package main

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "inferlean-cli-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp home: %v\n", err)
		os.Exit(1)
	}

	prevHome, hadHome := os.LookupEnv("HOME")
	prevUserProfile, hadUserProfile := os.LookupEnv("USERPROFILE")
	if err := os.Setenv("HOME", tmpHome); err != nil {
		fmt.Fprintf(os.Stderr, "set HOME: %v\n", err)
		_ = os.RemoveAll(tmpHome)
		os.Exit(1)
	}
	if err := os.Setenv("USERPROFILE", tmpHome); err != nil {
		fmt.Fprintf(os.Stderr, "set USERPROFILE: %v\n", err)
		_ = os.RemoveAll(tmpHome)
		os.Exit(1)
	}
	if err := os.Setenv(inferleanCrashlyticsDisableEnv, "true"); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", inferleanCrashlyticsDisableEnv, err)
		_ = os.RemoveAll(tmpHome)
		os.Exit(1)
	}

	exitCode := m.Run()

	if hadHome {
		_ = os.Setenv("HOME", prevHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	if hadUserProfile {
		_ = os.Setenv("USERPROFILE", prevUserProfile)
	} else {
		_ = os.Unsetenv("USERPROFILE")
	}
	_ = os.Unsetenv(inferleanCrashlyticsDisableEnv)
	_ = os.RemoveAll(tmpHome)
	os.Exit(exitCode)
}
