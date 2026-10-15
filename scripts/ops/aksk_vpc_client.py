#!/usr/bin/env python3
"""ANI tenant Network HMAC client (24 operations), using only the stdlib.

sign_network signs exact body bytes and a sorted form-encoded query. Mutations
must provide UTF-8 JSON bytes; no credentials or canonical request are printed.
The existing sign_vpc fixed vector and get_vpc entrypoint remain supported.
"""
import hashlib
import hmac
import json
import os
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


_NETWORK_ROUTES = [
    ("POST", r"/api/v1/networks/vpcs", "CreateVPC"),
    ("GET", r"/api/v1/networks/vpcs/vpc_[0-9a-f]{32}", "GetVPC"),
    ("GET", r"/api/v1/networks/vpcs", "ListVPCs"),
    ("DELETE", r"/api/v1/networks/vpcs/vpc_[0-9a-f]{32}", "DeleteVPC"),
    ("GET", r"/api/v1/networks/operations/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", "GetOperation"),
    ("POST", r"/api/v1/networks/subnets", "CreateSubnet"),
    ("GET", r"/api/v1/networks/subnets/subnet_[0-9a-f]{32}", "GetSubnet"),
    ("GET", r"/api/v1/networks/subnets", "ListSubnets"),
    ("DELETE", r"/api/v1/networks/subnets/subnet_[0-9a-f]{32}", "DeleteSubnet"),
    ("POST", r"/api/v1/networks/eips", "CreateEIP"),
    ("GET", r"/api/v1/networks/eips/eip_[0-9a-f]{32}", "GetEIP"),
    ("GET", r"/api/v1/networks/eips", "ListEIPs"),
    ("DELETE", r"/api/v1/networks/eips/eip_[0-9a-f]{32}", "DeleteEIP"),
    ("GET", r"/api/v1/networks/vpcs/vpc_[0-9a-f]{32}/snat", "GetVPCSnat"),
    ("POST", r"/api/v1/networks/vpcs/vpc_[0-9a-f]{32}/snat/bindings", "BindVPCSnat"),
    ("GET", r"/api/v1/networks/snat/bindings/snat_[0-9a-f]{32}", "GetVPCSnatBinding"),
    ("PATCH", r"/api/v1/networks/snat/bindings/snat_[0-9a-f]{32}", "SetVPCSnatEnabled"),
    ("DELETE", r"/api/v1/networks/snat/bindings/snat_[0-9a-f]{32}", "DeleteVPCSnatBinding"),
    ("POST", r"/api/v1/networks/load-balancers", "CreateLoadBalancer"),
    ("GET", r"/api/v1/networks/load-balancers/lb_[0-9a-f]{32}", "GetLoadBalancer"),
    ("GET", r"/api/v1/networks/load-balancers", "ListLoadBalancers"),
    ("PATCH", r"/api/v1/networks/load-balancers/lb_[0-9a-f]{32}", "UpdateLoadBalancer"),
    ("DELETE", r"/api/v1/networks/load-balancers/lb_[0-9a-f]{32}", "DeleteLoadBalancer"),
    ("GET", r"/api/v1/networks/load-balancers/operations/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", "GetLoadBalancerOperation"),
]
_LIST_FIELDS = {
    "ListVPCs": {"name", "state", "page", "pageSize"},
    "ListEIPs": {"name", "state", "limit", "cursor"},
    "ListSubnets": {"vpc_id", "name", "state", "limit", "cursor"},
    "ListLoadBalancers": {"name", "vpc_id", "subnet_id", "exposure", "state", "limit", "cursor"},
}


def sign_network(access_key, secret_key, method, path, query=None, body=b"", timestamp=None):
    """Return (canonical request target, headers) for an allowlisted operation.

    query is a mapping of the documented scalar fields. body is the same bytes
    that must be passed to urllib.request.Request(data=...), without re-encoding.
    """
    if not re.fullmatch(r"ak-[A-Za-z0-9_-]+", access_key):
        raise ValueError("invalid access key format")
    if not secret_key or secret_key != secret_key.strip():
        raise ValueError("secret key is empty or has surrounding whitespace")
    operation = next((op for verb, pattern, op in _NETWORK_ROUTES
                      if verb == method and re.fullmatch(pattern, path)), None)
    if operation is None:
        raise ValueError("unsupported Network method or noncanonical path")
    if not isinstance(body, bytes):
        raise ValueError("body must be the exact UTF-8 JSON bytes")
    if len(body) > 65536:
        raise ValueError("Network body exceeds 64 KiB")
    if method in ("POST", "PATCH"):
        try:
            payload = json.loads(body.decode("utf-8"), object_pairs_hook=_unique_fields)
        except (UnicodeError, ValueError) as exc:
            raise ValueError("mutation body must be unambiguous UTF-8 JSON") from exc
        if not isinstance(payload, dict):
            raise ValueError("mutation body must be a JSON object")
    elif body:
        raise ValueError("GET and DELETE accept no body")
    if query is None:
        query = {}
    if not isinstance(query, dict):
        raise ValueError("query must be a mapping of scalar fields")
    allowed = _LIST_FIELDS.get(operation, set())
    for key, value in query.items():
        if key not in allowed or not isinstance(value, (str, int)) or isinstance(value, bool):
            raise ValueError("unsupported or ambiguous query field")
        value = str(value)
        if len(value) > 4096:
            raise ValueError("query value too long")
        if key in ("vpc_id", "subnet_id") and value and not re.fullmatch(key[:-3] + r"_[0-9a-f]{32}", value):
            raise ValueError("invalid query ID")
        if key in {"page", "pageSize"}:
            maximum = 100 if key == "pageSize" else 4294967295
            if not re.fullmatch(r"[1-9][0-9]*", value) or int(value) > maximum:
                raise ValueError(key + " is out of range")
        if key == "limit" and (not re.fullmatch(r"[1-9][0-9]*", value) or int(value) > 100):
            raise ValueError("limit must be 1..100")
    canonical_query = urllib.parse.urlencode(sorted(query.items()))
    if len(canonical_query) > 8192:
        raise ValueError("Network query exceeds 8 KiB")
    ts = str(int(time.time()) if timestamp is None else timestamp)
    if not re.fullmatch(r"[1-9][0-9]*", ts) or int(ts) > 2**63 - 1:
        raise ValueError("timestamp must be positive Unix seconds")
    canonical = "\n".join([
        "ANI-HMAC-SHA256",
        method,
        path,
        canonical_query,  # Keep the line when the query is empty.
        access_key,
        ts,
        hashlib.sha256(body).hexdigest(),
    ]).encode("utf-8")  # No trailing newline.
    signature = hmac.new(
        secret_key.encode("utf-8"), canonical, hashlib.sha256
    ).hexdigest()
    headers = {
        "X-Access-Key": access_key,
        "X-Timestamp": ts,
        "X-Signature": signature,
        "Accept": "application/json",
    }
    if method in ("POST", "PATCH"):
        headers["Content-Type"] = "application/json"
    return path + ("?" + canonical_query if canonical_query else ""), headers


def _unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = value
    return result


def sign_vpc(access_key, secret_key, vpc_id, timestamp=None):
    return sign_network(access_key, secret_key, "GET", "/api/v1/networks/vpcs/" + vpc_id, timestamp=timestamp)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request_network(base_url, access_key, secret_key, method, path, query=None, body=b""):
    u = urllib.parse.urlsplit(base_url)
    if (u.scheme not in ("https", "http") or not u.hostname
            or u.username is not None or u.password is not None
            or u.path not in ("", "/") or u.query or u.fragment):
        raise ValueError("base URL must be an origin, e.g. https://host:port")
    if u.scheme == "http" and os.getenv("ANI_ALLOW_HTTP_FOR_TEST") != "1":
        raise ValueError("HTTP requires ANI_ALLOW_HTTP_FOR_TEST=1 in an isolated lab")
    target, headers = sign_network(access_key, secret_key, method, path, query, body)
    url = urllib.parse.urlunsplit((u.scheme, u.netloc, "", "", "")) + target
    req = urllib.request.Request(url, headers=headers, method=method, data=body if body else None)
    opener = urllib.request.build_opener(
        NoRedirect(),
        urllib.request.HTTPSHandler(context=ssl.create_default_context()),
    )
    with opener.open(req, timeout=10) as response:
        result = json.load(response)
    return result


def get_vpc(base_url, access_key, secret_key, vpc_id):
    result = request_network(base_url, access_key, secret_key, "GET", "/api/v1/networks/vpcs/" + vpc_id)
    if not isinstance(result, dict) or result.get("id") != vpc_id:
        raise ValueError("response does not contain the requested VPC")
    return result


def self_test():
    _, headers = sign_vpc(
        "ak-doc-example", "sk-doc-example-not-a-real-secret",
        "vpc_0123456789abcdef0123456789abcdef", 1700000000,
    )
    expected = "7df61f06b799ae477b51975097825e2e154ff62dffc46842964a51ed1afaebeb"
    if not hmac.compare_digest(headers["X-Signature"], expected):
        raise RuntimeError("signature vector mismatch")
    print("signature self-test: PASS (offline only)")


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        self_test()
    elif sys.argv[1:]:
        raise SystemExit("usage: python3 aksk_vpc_client.py [--self-test]")
    else:
        try:
            result = get_vpc(
                os.environ["ANI_BASE_URL"], os.environ["ANI_ACCESS_KEY"],
                os.environ["ANI_SECRET_KEY"], os.environ["ANI_VPC_ID"],
            )
            print(json.dumps(result, ensure_ascii=False, indent=2))
        except urllib.error.HTTPError as exc:
            raise SystemExit("Governance returned HTTP " + str(exc.code)) from None
        except (KeyError, ValueError, urllib.error.URLError) as exc:
            raise SystemExit(str(exc)) from None
