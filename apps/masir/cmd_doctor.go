package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gozargah/Masir/libs/masir-core/doctor"
	"github.com/Gozargah/Masir/libs/masir-core/paths"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// cmdDoctor gathers what the daemon reports, then runs the read-only checks.
// Nothing here changes state: every problem comes back as a suggested command.
func cmdDoctor(a args) error {
	env := doctor.Env{CLIVersion: version}
	if c, ok := runningDaemon(); ok {
		env.DaemonRunning = true
		ctx, cancel := rpc()
		defer cancel()
		if res, err := c.GetStatus(ctx, &pb.GetStatusRequest{}); err != nil {
			// The socket answered but the status call did not: keep that as an
			// unknown state instead of pretending the daemon is healthy.
			env.StatusError = err.Error()
		} else if st := res.GetStatus(); st != nil {
			env.DaemonVersion = st.GetVersion()
			env.XrayVersion = st.GetXrayVersion()
			env.XrayRunning = st.GetXrayRunning()
			env.TunRunning = st.GetTunRunning()
			env.ProxyAddress = st.GetProxyAddress()
			env.LastError = st.GetLastError()
			if s := st.GetSettings(); s != nil {
				env.TunGateway = s.GetTunGateway()
				env.Power = s.GetPower()
			}
		} else {
			env.StatusError = "the status reply was empty"
		}
	}

	if env.ProxyAddress == "" {
		// The daemon is down, but the port it would bind is still worth testing:
		// that is exactly when another proxy can be holding it.
		env.ProxyAddress = configuredProxyAddress()
	}
	reports := doctor.Run(appCtx, env)
	worst := doctor.Worst(reports)
	if a.boolean("json", false) {
		type row struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Group  string `json:"group"`
			State  string `json:"state"`
			Detail string `json:"detail"`
			Fix    string `json:"fix,omitempty"`
		}
		rows := make([]row, 0, len(reports))
		for _, r := range reports {
			rows = append(rows, row{r.ID, r.Title, r.Group, r.State.String(), r.Detail, r.Fix})
		}
		out := map[string]any{"version": version, "worst": worst.String(), "checks": rows}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	} else {
		label := "v" + version
		if version == "" || version == "dev" {
			label = "dev build"
		}
		printDoctor(reports, a.boolean("verbose", false), label)
	}
	switch worst {
	case doctor.Fail:
		exitStatus = 2
	case doctor.Warn:
		exitStatus = 1
	}
	return nil
}

// configuredProxyAddress reads the saved settings, for the case where the
// daemon is down and cannot report its address.
func configuredProxyAddress() string {
	dir, err := paths.ConfigDirNoCreate()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		ProxyPort int `json:"proxyPort"`
	}
	if json.Unmarshal(data, &s) != nil || s.ProxyPort <= 0 || s.ProxyPort > 65535 {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", s.ProxyPort)
}
