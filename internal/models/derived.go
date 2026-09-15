package models

import (
	"fmt"
	"math"

	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// DerivedManualStats returns the ratio and buffer implied by typed-in
// uploaded and downloaded values — for whichever of the two the user has NOT
// typed themselves. Nearly every tracker defines them the same way
// (uploaded ÷ downloaded, uploaded − downloaded), so asking for them is asking
// the user to do arithmetic Yata can do; a tracker with its own formula is
// handled by typing the value, which then wins outright.
//
// Computed on every read rather than written into ManualStats, so changing
// uploaded later moves the ratio with it and nothing stale is ever stored.
//
// The frontend previews the same two formulas live in the edit form
// (manualDerived in modals.ts); keep them in step.
func DerivedManualStats(stats map[string]string) map[string]string {
	up := parse.SizeToGiB(stats["uploaded"])
	down := parse.SizeToGiB(stats["downloaded"])
	if up == nil || down == nil {
		return nil
	}
	out := map[string]string{}
	if stats["ratio"] == "" {
		switch {
		case *down > 0:
			out["ratio"] = fmt.Sprintf("%.2f", *up / *down)
		case *up > 0:
			out["ratio"] = "Infinity" // nothing downloaded yet — what the fetchers report too
		}
	}
	if stats["buffer"] == "" {
		out["buffer"] = formatSignedGiB(*up - *down)
	}
	return out
}

// formatSignedGiB renders a GiB figure as the sizes elsewhere read
// ("4.30 TiB"), keeping the sign: a buffer is the one size that is often
// negative, and BytesToSize floors at zero.
func formatSignedGiB(gib float64) string {
	if math.Abs(gib) < 0.005 {
		return "0.00 GiB"
	}
	s := parse.BytesToSize(int64(math.Round(math.Abs(gib) * 1024 * 1024 * 1024)))
	if gib < 0 {
		return "-" + s
	}
	return s
}
