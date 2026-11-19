package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	xgeo "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func TestCheckRulesKeepsDiagnosticsOffStdout(t *testing.T) {
	if os.Getenv("MASIR_TEST_RULE_CHECK") == "1" {
		if err := CheckRules(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	dir := t.TempDir()
	geo, err := proto.Marshal(&xgeo.GeoSiteList{Entry: []*xgeo.GeoSite{{
		Code: "FIXTURE", Domain: []*xgeo.Domain{{Value: "example.com"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), geo, 0o600); err != nil {
		t.Fatal(err)
	}
	req := fmt.Sprintf(`{"assetDir":%q,"rules":[
		{"tag":"valid","match":{"domain":["geosite:fixture"]}},
		{"tag":"broken","match":{"domain":["geosite:missing"]}}
	]}`, dir)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckRulesKeepsDiagnosticsOffStdout$")
	cmd.Env = append(os.Environ(), "MASIR_TEST_RULE_CHECK=1")
	cmd.Stdin = bytes.NewBufferString(req)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("check failed: %v (%s)", err, &stderr)
	}
	var reports []struct {
		Tag     string
		Missing []string
	}
	if err := json.Unmarshal(out, &reports); err != nil {
		t.Fatalf("stdout = %q, want a JSON report array: %v", out, err)
	}
	if len(reports) != 1 || reports[0].Tag != "broken" || len(reports[0].Missing) != 1 || reports[0].Missing[0] != "geosite:missing" {
		t.Fatalf("stdout = %q, want just the missing category report", out)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("geodata mph domain matcher cache MISS")) {
		t.Fatalf("stderr = %q, want the Xray cache diagnostic", &stderr)
	}
}
