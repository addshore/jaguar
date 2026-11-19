// Copyright (C) 2026 Toit contributors.
// Use of this source code is governed by an MIT-style license that can be
// found in the LICENSE file.

package commands

import (
	"testing"

	"go.bug.st/serial/enumerator"
)

func TestSameMonitorPortName(t *testing.T) {
	tests := []struct {
		name, goos, a, b string
		want             bool
	}{
		{"darwin dial-in alias", "darwin", "/dev/cu.usbmodem123", "/dev/tty.usbmodem123", true},
		{"darwin callout alias", "darwin", "/dev/tty.usbmodem123", "/dev/cu.usbmodem123", true},
		{"darwin different devices", "darwin", "/dev/cu.usbmodem123", "/dev/tty.usbmodem456", false},
		{"darwin case-sensitive", "darwin", "/dev/cu.usbmodemABC", "/dev/tty.usbmodemabc", false},
		{"darwin requires device path", "darwin", "/dev/cu.usbmodem123", "usbmodem123", false},
		{"windows case-insensitive", "windows", "COM26", "com26", true},
		{"windows different ports", "windows", "COM26", "COM27", false},
		{"linux exact match", "linux", "/dev/ttyACM0", "/dev/ttyACM0", true},
		{"linux no darwin aliases", "linux", "/dev/cu.usbmodem123", "/dev/tty.usbmodem123", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameMonitorPortName(tt.a, tt.b, tt.goos); got != tt.want {
				t.Fatalf("sameMonitorPortName(%q, %q, %q) = %v; want %v", tt.a, tt.b, tt.goos, got, tt.want)
			}
		})
	}
}

func TestMatchingMonitorUSB(t *testing.T) {
	previous := &enumerator.PortDetails{
		Name: "COM23", IsUSB: true, VID: "303A", PID: "4001", SerialNumber: "TOITS311C2904D06954",
	}
	next := *previous
	next.Name = "COM26"
	next.VID = "303a"
	next.SerialNumber = "toits311c2904d06954"
	other := next
	other.SerialNumber = "TOITS31000000000000"
	ports := []*enumerator.PortDetails{&other, &next}
	if got := matchingMonitorUSB(ports, previous); got != &next {
		t.Fatal("did not select the same USB identity on its new port")
	}
	for _, change := range []func(*enumerator.PortDetails){
		func(p *enumerator.PortDetails) { p.IsUSB = false },
		func(p *enumerator.PortDetails) { p.VID = "1234" },
		func(p *enumerator.PortDetails) { p.PID = "1234" },
		func(p *enumerator.PortDetails) { p.SerialNumber = "TOITS31000000000000" },
	} {
		candidate := next
		change(&candidate)
		if got := matchingMonitorUSB([]*enumerator.PortDetails{&candidate}, previous); got != nil {
			t.Fatalf("accepted a different device: %+v", candidate)
		}
	}
}
