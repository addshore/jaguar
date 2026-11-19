// Copyright (C) 2021 Toitware ApS. All rights reserved.
// Use of this source code is governed by an MIT-style license that can be
// found in the LICENSE file.

package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

func MonitorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "monitor",
		Short:        "Monitor the serial output of an ESP32",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			port, err := cmd.Flags().GetString("port")
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("port") {
				if port, err = checkPort(port, false); err != nil {
					return err
				}
			}

			baud, err := cmd.Flags().GetUint("baud")
			if err != nil {
				return err
			}
			shouldProxy, err := cmd.Flags().GetBool("proxy")
			if err != nil {
				return err
			}
			if shouldProxy && !cmd.Flags().Changed("baud") {
				baud = defaultProxyBaudRate
			}

			attach, err := cmd.Flags().GetBool("attach")
			if err != nil {
				return err
			}

			pretty, err := cmd.Flags().GetBool("force-pretty")
			if err != nil {
				return err
			}

			plain, err := cmd.Flags().GetBool("force-plain")
			if err != nil {
				return err
			}

			dev, port, err := openMonitorPort(ctx, port, int(baud), attach, shouldProxy)
			if err != nil {
				return err
			}
			defer dev.Close()
			fmt.Printf("Starting serial monitor of port '%s' ...\n", port)

			var logReader io.Reader = dev

			if shouldProxy {
				ch1, ch2 := multiplexReader(dev)
				logReader = ch1
				go func() {
					if err := runUartProxy(dev, ch2); err != nil {
						fmt.Printf("[jaguar.uart] ERROR: proxy failed: %v\n", err)
					}
				}()
			}

			scanner := bufio.NewScanner(logReader)

			envelope, err := cmd.Flags().GetString("envelope")
			if err != nil {
				return err
			}

			// Create a context-aware decoder that can be interrupted.
			decoder := NewDecoder(scanner, ctx, envelope)
			done := make(chan error, 1)
			go func() {
				decoder.decode(pretty, plain)
				done <- scanner.Err()
			}()

			// Wait for either completion or context cancellation.
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	cmd.Flags().StringP("port", "p", ConfiguredPort(), "port to monitor")
	cmd.Flags().BoolP("attach", "a", false, "attach to the serial output without rebooting it")
	cmd.Flags().BoolP("force-pretty", "r", false, "force output to use terminal graphics")
	cmd.Flags().BoolP("force-plain", "l", false, "force output to use plain ASCII text")
	cmd.Flags().Uint("baud", 115200, "the baud rate for serial monitoring; defaults to 921600 in proxy mode")
	cmd.Flags().Bool("proxy", false, "proxy the connected device to the local network")
	cmd.Flags().String("envelope", "", "name or path of the firmware envelope")
	return cmd
}

// The S31 USB-OTG console uses this prefix followed by the chip's MAC address.
const s31ConsoleSerialPrefix = "TOITS31"

func openMonitorPort(ctx context.Context, port string, baud int, attach, shouldProxy bool) (*serialPort, string, error) {
	resolved := port
	if path, err := filepath.EvalSymlinks(port); err == nil {
		resolved = path
	}
	hasModemLines := !strings.HasPrefix(resolved, "/dev/pts/")
	reboot := !attach && hasModemLines
	var usb *enumerator.PortDetails
	if hasModemLines {
		usb = lookupUSBConsole(resolved)
	}
	if usb != nil && shouldProxy {
		return nil, port, fmt.Errorf("the ESP32-S31 USB console only carries logs; use a UART connection for --proxy")
	}

	dev, err := serialOpen(port, baud, hasModemLines && usb == nil)
	if err != nil || !reboot {
		return dev, port, err
	}
	if usb != nil {
		// The S31 OTG console recognizes 1200 8E2 as a normal-boot reset request.
		err := dev.SetMode(&serial.Mode{
			BaudRate: 1200,
			DataBits: 8,
			Parity:   serial.EvenParity,
			StopBits: serial.TwoStopBits,
		})
		dev.Close()
		if err != nil {
			return nil, port, fmt.Errorf("could not request an ESP32-S31 USB reset on '%s': %w", port, err)
		}
		fmt.Printf("Waiting for the ESP32-S31 USB console to reconnect ...\n")
		return reopenMonitorUSB(ctx, usb, baud)
	}
	dev.Reboot()
	return dev, port, nil
}

func lookupUSBConsole(port string) *enumerator.PortDetails {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil
	}
	for _, candidate := range ports {
		if candidate.IsUSB && strings.HasPrefix(strings.ToUpper(candidate.SerialNumber), s31ConsoleSerialPrefix) &&
			sameMonitorPortName(candidate.Name, port, runtime.GOOS) {
			return candidate
		}
	}
	return nil
}

// Darwin exposes separate callout (cu) and dial-in (tty) nodes for the same
// serial device. The enumerator returns the callout node, but either can be
// passed explicitly to monitor.
func sameMonitorPortName(a, b, goos string) bool {
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	if goos == "darwin" {
		if strings.HasPrefix(a, "/dev/tty.") {
			a = "/dev/cu." + strings.TrimPrefix(a, "/dev/tty.")
		}
		if strings.HasPrefix(b, "/dev/tty.") {
			b = "/dev/cu." + strings.TrimPrefix(b, "/dev/tty.")
		}
	}
	return a == b
}

func reopenMonitorUSB(ctx context.Context, previous *enumerator.PortDetails, baud int) (*serialPort, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	detached := false

	for {
		ports, err := enumerator.GetDetailedPortsList()
		if err == nil {
			next := matchingMonitorUSB(ports, previous)
			if next == nil {
				detached = true
			} else if detached || next.Name != previous.Name {
				dev, err := serialOpen(next.Name, baud, false)
				if err == nil {
					return dev, next.Name, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, previous.Name, fmt.Errorf("ESP32-S31 USB console on '%s' did not reconnect after reset: %w", previous.Name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func matchingMonitorUSB(ports []*enumerator.PortDetails, previous *enumerator.PortDetails) *enumerator.PortDetails {
	for _, port := range ports {
		if port.IsUSB && strings.EqualFold(port.VID, previous.VID) &&
			strings.EqualFold(port.PID, previous.PID) && strings.EqualFold(port.SerialNumber, previous.SerialNumber) {
			return port
		}
	}
	return nil
}

func serialOpen(port string, baud int, setModemLines bool) (*serialPort, error) {
	mode := &serial.Mode{BaudRate: baud}
	if setModemLines {
		// Keep both lines asserted on open to avoid accidentally rebooting UART
		// devices. Pseudo-terminals and the S31 console do not need this setup.
		mode.InitialStatusBits = &serial.ModemOutputBits{
			RTS: true,
			DTR: true,
		}
	}
	dev, err := serial.Open(port, mode)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("the port '%s' was not found", port)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open port '%s': %w", port, err)
	}

	return &serialPort{dev}, err
}

type serialPort struct {
	serial.Port
}

func (s serialPort) Read(buf []byte) (n int, err error) {
	n, err = s.Port.Read(buf)
	if err == nil && n == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}

func (s *serialPort) Close() error {
	if s.Port != nil {
		return s.Port.Close()
	}
	return nil
}

func (s *serialPort) Reboot() {
	s.SetDTR(false)
	s.SetRTS(true)
	time.Sleep(100 * time.Millisecond)
	s.SetRTS(false)
}
