package daemon

import (
	"io"
	"log"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/rulecheck"
	xlog "github.com/xtls/xray-core/common/log"
)

// CheckRules is `masir daemon check`: a running daemon asks a newly downloaded masir,
// over stdin, whether it can build the user's rules with the geo data on disk, before
// that version is installed. It writes the rules it can't, as JSON, and changes nothing.
func CheckRules(in io.Reader, out io.Writer) error {
	// This short-lived helper reserves stdout for the JSON reply. Xray logs
	// from its rule builders even when it has no running instance.
	xlog.RegisterHandler(ruleCheckLogger{})
	return rulecheck.Dry(in, out)
}

type ruleCheckLogger struct{}

func (ruleCheckLogger) Handle(msg xlog.Message) { log.Print(msg.String()) }
