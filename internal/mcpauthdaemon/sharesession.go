package mcpauthdaemon

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// ShareSessionBinding captures the validated private share and its trusted
// publication path before serving. Neither identity comes from a request.
// The backend socket is outside the publication directory; core supplies the
// endpoint locator privately. The front still owns authentication.
type ShareSessionBinding struct {
	container string
	principal string
}

func BindShareSession(share mcpauth.Share, container, publicationPath string) ShareSessionBinding {
	if share.Jail == "" || share.Jail != container || !filepath.IsAbs(publicationPath) || filepath.Clean(publicationPath) != publicationPath || filepath.Ext(publicationPath) != ".endpoint" {
		return ShareSessionBinding{}
	}
	principal := svcendpoint.PublicationJailID(publicationPath)
	if principal == "unknown" {
		return ShareSessionBinding{}
	}
	return ShareSessionBinding{container: share.Jail, principal: principal}
}

func (b ShareSessionBinding) accepts(share mcpauth.Share, s *hostservice.Session) bool {
	return b.container != "" && b.principal != "" && b.container == share.Jail &&
		s.Fronted && s.JailID == b.principal
}
