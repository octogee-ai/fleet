package main

import (
	"runtime/debug"
	"testing"
)

func moduleVersion(version string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/octogee-ai/octogee-fleet", Version: version}}, true
	}
}

func TestAReleaseBuildReportsTheTagItWasStampedWith(t *testing.T) {
	if got := versionOf("v0.1.0", moduleVersion("v0.0.0-20261113120000-4483158abcde")); got != "v0.1.0" {
		t.Fatalf("version = %q, want v0.1.0", got)
	}
}

func TestGoInstallAtATagReportsTheTag(t *testing.T) {
	if got := versionOf("", moduleVersion("v0.1.0")); got != "v0.1.0" {
		t.Fatalf("version = %q, want v0.1.0", got)
	}
}

func TestGoInstallAtACommitReportsItsPseudoVersion(t *testing.T) {
	pseudo := "v0.0.0-20261113120000-4483158abcde"
	if got := versionOf("", moduleVersion(pseudo)); got != pseudo {
		t.Fatalf("version = %q, want %q", got, pseudo)
	}
}

func TestABuildWithNoModuleVersionReportsDev(t *testing.T) {
	for name, read := range map[string]func() (*debug.BuildInfo, bool){
		"devel":         moduleVersion("(devel)"),
		"empty":         moduleVersion(""),
		"no build info": func() (*debug.BuildInfo, bool) { return nil, false },
	} {
		if got := versionOf("", read); got != "dev" {
			t.Errorf("%s: version = %q, want dev", name, got)
		}
	}
}
