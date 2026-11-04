// Smoke test against a real environment:
//
//	SBBOX_API_KEY=gsk-… [SBBOX_BASE_URL=…] go run ./smoke
//
// create → exec → files → run code → freeze (wait) → resume → kill.
// Output never includes the key.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sbbox "github.com/genspark-ai/sb-box-sdk/go"
)

func step(name string, err error) {
	if err != nil {
		fmt.Printf("FAIL %-10s %s\n", name, sbbox.Redact(err.Error()))
		os.Exit(1)
	}
	fmt.Printf("ok   %s\n", name)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client, err := sbbox.New(sbbox.WithUserAgentSuffix("smoke"))
	step("client", err)

	lim, err := client.Limits(ctx)
	step("limits", err)
	fmt.Printf("     plan=%s running=%d/%d\n", lim.Plan, lim.Usage.RunningSandboxes, lim.Limits.MaxConcurrentSandboxes)

	sb, err := client.Sandboxes.Create(ctx, sbbox.CreateOptions{Template: "base", Timeout: 900, Metadata: map[string]string{"purpose": "go-sdk-smoke"}})
	step("create", err)
	fmt.Printf("     id=%s state=%s region=%s\n", sb.ID, sb.State, sb.Region)
	defer func() {
		kctx, kcancel := context.WithTimeout(context.Background(), time.Minute)
		defer kcancel()
		step("kill", sb.Kill(kctx))
	}()

	res, err := sb.Exec(ctx, sbbox.ShellCommand("echo smoke-$(id -un)"), nil)
	step("exec", err)
	if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, "smoke-") {
		step("exec-output", fmt.Errorf("unexpected result %+v", res))
	}

	st, err := sb.ExecStream(ctx, sbbox.ShellCommand("echo one; echo two >&2; exit 4"), nil)
	step("exec-stream", err)
	var exit int
	for ev, ok := st.Next(); ok; ev, ok = st.Next() {
		if ev.Kind == "exit" {
			exit = ev.ExitCode
		}
	}
	if st.Err() != nil || exit != 4 {
		step("exec-stream-exit", fmt.Errorf("err=%v exit=%d", st.Err(), exit))
	}

	payload := []byte("go sdk smoke " + time.Now().UTC().Format(time.RFC3339) + "\n")
	_, err = sb.Files.Write(ctx, "/tmp/smoke.txt", payload, "")
	step("file-write", err)
	back, err := sb.Files.Read(ctx, "/tmp/smoke.txt", "")
	step("file-read", err)
	if string(back) != string(payload) {
		step("file-roundtrip", fmt.Errorf("mismatch"))
	}
	ls, err := sb.Files.List(ctx, "/tmp", "")
	step("file-list", err)
	fmt.Printf("     %d entries in /tmp\n", len(ls))

	code, err := sb.RunCode(ctx, "1+1", nil)
	step("run-code", err)
	fmt.Printf("     results=%d error=%v\n", len(code.Results), code.Error)

	step("freeze", sb.Freeze(ctx, true))
	fmt.Printf("     state=%s\n", sb.State)
	step("resume", sb.Resume(ctx, 0))
	fmt.Printf("     state=%s\n", sb.State)

	res, err = sb.Exec(ctx, []string{"cat", "/tmp/smoke.txt"}, nil)
	step("exec-after-resume", err)
	if string(back) != res.Stdout {
		step("file-survives-freeze", fmt.Errorf("mismatch"))
	}
}
