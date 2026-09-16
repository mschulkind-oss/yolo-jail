package check

import (
	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// buildImageReal runs the real nix build check() needs and returns
// (storePath, stderrTail). The out-link + streaming
// spinner are elided (check only consumes the result); the store path is the
// resolved out-link.
//
// WHAT IT PROVES IS THE DEFAULT IMAGE, NOT THIS HOST'S NEXT LAUNCH.
// image.BuildOCIImage builds `image.ImageAttrDefault` (plus the copier)
// unconditionally, with the config's `packages:` in YOLO_EXTRA_PACKAGES. A launch
// that opts into store-delivered packages (`YOLO_STORE_PACKAGES=1`, C5) realizes
// neither of those: it builds `.#ociImageLean` with NO extras and gets the rest
// from `.#yoloImageExtras`. So on such a host a green Image section is a claim
// about an image the launch will not build, and says nothing about the two attrs
// it will. check has no store-packages notion at all today; closing this needs an
// attr parameter on BuildOCIImage, which is why the gap is written down here
// rather than patched at this call site.
func buildImageReal(repoRoot string, extraPackages []any) (string, []string) {
	return image.BuildOCIImage(repoRoot, extraPackages)
}

// itoa avoids strconv import churn across the check package.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
