package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunVersionHelpAndErrors(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		wantCode int
		want     string
	}{
		{args: nil, want: "satchel setup"},
		{args: []string{"version"}, want: "satchel " + Version},
		{args: []string{"unknown"}, wantCode: 2, want: "unknown command"},
		{args: []string{"config"}, wantCode: 2, want: "satchel config show"},
		{args: []string{"mcp", "vision"}, wantCode: 2, want: "mcp takes no arguments"},
		{args: []string{"mcp-config", "--split"}, wantCode: 1, want: "mcp-config takes no arguments"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), tc.args, nil, &out, &errOut); code != tc.wantCode {
			t.Errorf("Run(%v) code = %d, want %d", tc.args, code, tc.wantCode)
		}
		if combined := out.String() + errOut.String(); !strings.Contains(combined, tc.want) {
			t.Errorf("Run(%v) output = %q, want %q", tc.args, combined, tc.want)
		}
	}
}
