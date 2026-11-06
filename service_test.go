package main

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestServiceInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	executable := "/path/with & <special> characters/daemon"
	getExecutable := func() (string, error) { return executable, nil }
	if err := service(home, []string{"install"}, getExecutable, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plist struct {
		Values []struct {
			Key string `xml:"key"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		t.Fatalf("invalid plist XML: %v", err)
	}
	if !strings.Contains(string(data), "/path/with &amp; &lt;special&gt; characters/daemon") {
		t.Fatalf("executable was not escaped: %s", data)
	}
	if err := service(home, []string{"install"}, getExecutable, nil); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected existing plist error, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("existing plist changed: %v", err)
	}
}

func TestServiceLaunchctl(t *testing.T) {
	home := t.TempDir()
	domain := "gui/" + strconv.Itoa(os.Getuid())
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"start", []string{"bootstrap", domain, plist}},
		{"restart", []string{"kill", "SIGTERM", domain + "/" + serviceLabel}},
		{"stop", []string{"bootout", domain + "/" + serviceLabel}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			failure := errors.New("launchctl failed")
			err := service(home, []string{tc.name}, nil, func(args ...string) error {
				got = args
				return failure
			})
			if !reflect.DeepEqual(got, tc.want) || !errors.Is(err, failure) {
				t.Fatalf("got args %q and error %v; want %q and %v", got, err, tc.want, failure)
			}
		})
	}
}

func TestServiceArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"start", "extra"}, {"unknown"}} {
		if err := service(t.TempDir(), args, nil, nil); err == nil {
			t.Errorf("service(%q) succeeded", args)
		}
	}
}
