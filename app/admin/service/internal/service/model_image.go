package service

import (
	"context"
	"strings"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
)

// ModelImageCatalog exposes only the already authorized catalog read. Model
// defaults never require, return or persist registry pull credentials.
type ModelImageCatalog interface {
	GetImage(context.Context, string, string, *imagev1.GetImageRequest) (*imagev1.GetImageResponse, error)
}

func (s *ModelService) modelImageReference(ctx context.Context, tenant, actor, id, scope, raw string) (string, error) {
	if id == "" && scope == "" {
		if strings.TrimSpace(raw) == "" {
			return "", kratoserrors.BadRequest("INVALID_MODEL_IMAGE", "engine_image or a catalog image selection is required")
		}
		return raw, nil
	}
	if !imageIDPattern.MatchString(id) {
		return "", kratoserrors.BadRequest("INVALID_MODEL_IMAGE", "recommended_image_id and scope are required together")
	}
	parsed, err := imageScope(scope)
	if err != nil {
		return "", err
	}
	if s.images == nil {
		return "", kratoserrors.ServiceUnavailable("IMAGE_UNAVAILABLE", "Image catalog is not configured")
	}
	reply, err := s.images.GetImage(ctx, tenant, actor, &imagev1.GetImageRequest{ImageId: id, Scope: parsed})
	if err != nil {
		return "", mapImageError(err)
	}
	if err := validateImageReply(reply.GetImage(), tenant, parsed, id); err != nil {
		return "", err
	}
	if reply.Image.UnregisteredAt != nil {
		return "", kratoserrors.BadRequest("INVALID_MODEL_IMAGE", "selected image is unregistered")
	}
	ref := reply.Image.ResolvedReference
	if raw != "" && raw != ref {
		return "", kratoserrors.BadRequest("INVALID_MODEL_IMAGE", "engine_image conflicts with selected image")
	}
	// IMG-KNOWN-P2-001 remains DEFERRED: this existing catalog-before-save
	// order can block replay after catalog changes or loses availability.
	return ref, nil
}
