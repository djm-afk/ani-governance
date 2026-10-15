package service

import "github.com/go-kratos/kratos/v2/errors"

// Resolve defaults independently, preserving explicit zero in non-HTTP callers.
func resolveVPCPage(page, pageSize *uint32) (uint32, uint32, error) {
	p, size := uint32(1), uint32(20)
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		size = *pageSize
	}
	if p == 0 || size == 0 || size > 100 {
		return 0, 0, errors.BadRequest("INVALID_NETWORK_REQUEST", "page must be >= 1 and pageSize within 1..100")
	}
	return p, size, nil
}
