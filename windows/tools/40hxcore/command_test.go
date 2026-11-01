package hxcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommandChild(t *testing.T) {
	mode := os.Getenv("CMP40_COMMAND_TEST_CHILD")
	if mode == "" {
		return
	}
	fmt.Print("child-output")
	if mode == "hang" {
		time.Sleep(20 * time.Second)
	}
	os.Exit(0)
}

func TestRunOutTimeout(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMP40_COMMAND_TEST_CHILD", "normal")
	out, err := RunOutTimeout(5*time.Second, exe, "-test.run=^TestCommandChild$")
	if err != nil || !strings.Contains(out, "child-output") {
		t.Fatalf("normal command: %q / %v", out, err)
	}
	t.Setenv("CMP40_COMMAND_TEST_CHILD", "hang")
	start := time.Now()
	out, err = RunOutTimeout(3*time.Second, exe, "-test.run=^TestCommandChild$")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(out, "child-output") {
		t.Fatalf("hung command: %q / %v", out, err)
	}
	if time.Since(start) > 8*time.Second {
		t.Fatal("hung command exceeded the bounded wait")
	}
}
