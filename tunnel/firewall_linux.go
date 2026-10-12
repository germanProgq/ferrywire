package tunnel

import (
	"log/slog"
	"os/exec"
)

var rstDropRule = []string{"OUTPUT", "-p", "tcp", "--tcp-flags", "RST", "RST", "-j", "DROP"}

func deleteRSTDropRule() ([]byte, error) {
	return exec.Command("iptables", append([]string{"-D"}, rstDropRule...)...).CombinedOutput()
}

func ensureRSTDropRule() bool {
	for {
		if _, err := deleteRSTDropRule(); err != nil {
			break
		}
	}

	insertArgs := append([]string{"-I", "OUTPUT", "1"}, rstDropRule[1:]...)
	if out, err := exec.Command("iptables", insertArgs...).CombinedOutput(); err != nil {
		slog.Warn("failed to install iptables RST-drop rule; run: sudo iptables -I OUTPUT 1 -p tcp --tcp-flags RST RST -j DROP", "err", err, "output", string(out))
		return false
	}
	slog.Debug("installed iptables RST-drop rule at top of OUTPUT chain")
	return true
}

func removeRSTDropRule() {
	if out, err := deleteRSTDropRule(); err != nil {
		slog.Warn("failed to remove iptables RST-drop rule", "err", err, "output", string(out))
		return
	}
	slog.Debug("removed iptables RST-drop rule")
}
