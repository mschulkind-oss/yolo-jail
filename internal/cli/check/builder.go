package check

import (
	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// buildImageReal runs the real nix build check() needs and returns
// (storePath, stderrTail). The out-link + streaming
// spinner are elided (check only consumes the result); the store path is the
// resolved out-link.
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
