package tunnel

import (
	"os/exec"

	"github.com/germanProgq/ferrywire/utils"
)

func ensureRSTDropRule() {
	args := []string{"OUTPUT", "-p", "tcp", "--tcp-flags", "RST", "RST", "-j", "DROP"}

	for {
		if err := exec.Command("iptables", append([]string{"-D"}, args...)...).Run(); err != nil {
			break
		}
	}

	insertArgs := append([]string{"-I", "OUTPUT", "1"}, args[1:]...)
	if out, err := exec.Command("iptables", insertArgs...).CombinedOutput(); err != nil {
		utils.Debugf("[TUNNEL] Failed to auto-configure iptables RST-drop rule: %v (%s)", err, string(out))
		utils.Debugf("[TUNNEL] Run manually: sudo iptables -I OUTPUT 1 -p tcp --tcp-flags RST RST -j DROP")
	} else {
		utils.Debugf("[TUNNEL] Auto-configured iptables RST-drop rule at top of OUTPUT chain")
	}
}
