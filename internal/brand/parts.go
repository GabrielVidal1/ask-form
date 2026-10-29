package brand

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// darkMin is the lightest a primary gets in dark mode: a dark brand colour
// would vanish against the dark background, so it is lifted to this.
const darkMin = 58

// ParseColor reads a hex colour (#rgb or #rrggbb, the # optional) as
// "#rrggbb".
func ParseColor(s string) (string, error) {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil || len(h) != 6 {
		return "", fmt.Errorf("%q: a hex colour like #f97316", s)
	}
	return "#" + h, nil
}

// ImageFile reads a logo: an image file, as a data: URI.
func ImageFile(path string) (string, error) {
	return partFile(path, "image/", "an image (png, jpg, gif, webp, avif, svg, ico)")
}

// FontFile reads a font file (woff2, woff, ttf, otf) as a data: URI.
func FontFile(path string) (string, error) {
	return partFile(path, "font/", "a font file (woff2, woff, ttf, otf)")
}

func partFile(path, kind, want string) (string, error) {
	ct, ok := fileTypes[strings.ToLower(filepath.Ext(path))]
	if !ok || !strings.HasPrefix(ct, kind) {
		return "", fmt.Errorf("%s: %s", path, want)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(b) > MaxSize {
		return "", fmt.Errorf("%s: %d KB, the whole brand can be at most %d KB", path, len(b)>>10, MaxSize>>10)
	}
	return DataURI(ct, b), nil
}

// fontFormat is the format() hint @font-face wants for a font data: URI.
func fontFormat(uri string) string {
	switch {
	case strings.HasPrefix(uri, "data:font/woff2"):
		return "woff2"
	case strings.HasPrefix(uri, "data:font/woff"):
		return "woff"
	case strings.HasPrefix(uri, "data:font/otf"):
		return "opentype"
	}
	return "truetype"
}

// hexHSL converts "#rrggbb" to hue (degrees), saturation and lightness (%).
func hexHSL(hex string) (h, s, l float64) {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	r, g, b := float64(v>>16&0xff)/255, float64(v>>8&0xff)/255, float64(v&0xff)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	if d := hi - lo; d > 0 {
		if l > 0.5 {
			s = d / (2 - hi - lo)
		} else {
			s = d / (hi + lo)
		}
		switch hi {
		case r:
			h = math.Mod((g-b)/d+6, 6)
		case g:
			h = (b-r)/d + 2
		default:
			h = (r-g)/d + 4
		}
		h *= 60
	}
	return h, s * 100, l * 100
}

// triplet is an "H S% L%" theme token value.
func triplet(h, s, l float64) string {
	return fmt.Sprintf("%.0f %.0f%% %.0f%%", h, s, l)
}

// onColor is the text colour for a fill of h s l: white, as on ask-form's
// own orange, unless the fill is too light for it (a contrast under 2.5:1 —
// yellow, sky blue…), then the theme's near-black.
func onColor(h, s, l float64) string {
	if 1.05/(luminance(h, s, l)+0.05) >= 2.5 {
		return "0 0% 100%"
	}
	return "24 10% 10%"
}

// luminance is the WCAG relative luminance of an HSL colour.
func luminance(h, s, l float64) float64 {
	s, l = s/100, l/100
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	lin := func(v float64) float64 {
		v += m
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
