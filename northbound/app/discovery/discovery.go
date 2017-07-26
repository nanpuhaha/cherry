/*
 * Cherry - An OpenFlow Controller
 *
 * Copyright (C) 2015 Samjung Data Service, Inc. All rights reserved.
 * Kitae Kim <superkkt@sds.co.kr>
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License along
 * with this program; if not, write to the Free Software Foundation, Inc.,
 * 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.
 */

package discovery

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/superkkt/cherry/network"
	"github.com/superkkt/cherry/northbound/app"
	"github.com/superkkt/cherry/protocol"

	"github.com/op/go-logging"
)

var (
	logger = logging.MustGetLogger("discovery")

	// A locally administered MAC address (https://en.wikipedia.org/wiki/MAC_address#Universal_vs._local).
	myMAC = net.HardwareAddr([]byte{0x06, 0xff, 0x29, 0x34, 0x82, 0x87})
)

// XXX: The router should have gateway IP addresses on its interfaces to respond to our ARP probes.
type processor struct {
	app.BaseProcessor
	db Database

	mutex     sync.Mutex
	canceller map[string]context.CancelFunc // Key = Device ID.
}

type Database interface {
	// GetUndiscoveredHosts returns IP addresses whose physical location is still
	// undiscovered or staled more than expiration.
	GetUndiscoveredHosts(expiration time.Duration) ([]net.IP, error)

	// UpdateHostLocation updates the physical location of a host, whose MAC and IP
	// addresses are matched with mac and ip, to the port identified by swDPID and
	// portNum. updated will be true if its location has been actually updated.
	UpdateHostLocation(mac net.HardwareAddr, ip net.IP, swDPID uint64, portNum uint16) (updated bool, err error)

	// ResetHostLocationsByPort sets NULL to the host locations that belong to the
	// port specified by swDPID and portNum.
	ResetHostLocationsByPort(swDPID uint64, portNum uint16) error

	// ResetHostLocationsByDevice sets NULL to the host locations that belong to the
	// device specified by swDPID.
	ResetHostLocationsByDevice(swDPID uint64) error
}

func New(db Database) app.Processor {
	return &processor{
		db:        db,
		canceller: make(map[string]context.CancelFunc),
	}
}

func (r *processor) Name() string {
	return "Discovery"
}

func (r *processor) String() string {
	return fmt.Sprintf("%v", r.Name())
}

func (r *processor) OnDeviceUp(finder network.Finder, device *network.Device) error {
	r.removeARPSender(device.ID())
	r.addARPSender(device)

	// Propagate this event to the next processors.
	return r.BaseProcessor.OnDeviceUp(finder, device)
}

func (r *processor) addARPSender(device *network.Device) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Infinite loop.
		for {
			select {
			case <-ctx.Done():
				logger.Debugf("terminating the ARP sender: deviceID=%v", device.ID())
				return
			default:
			}

			if err := r.sendARPProbes(device); err != nil {
				logger.Errorf("failed to send ARP probes: %v", err)
				// Ignore this error and keep go on.
			}
			time.Sleep(time.Duration((100 + rand.Intn(5000))) * time.Millisecond)
		}
	}()
	r.canceller[device.ID()] = cancel
}

func (r *processor) sendARPProbes(device *network.Device) error {
	if device.IsClosed() {
		return fmt.Errorf("already closed deivce: id=%v", device.ID())
	}

	hosts, err := r.db.GetUndiscoveredHosts(1 * time.Minute)
	if err != nil {
		return err
	}
	for _, ip := range hosts {
		if err := device.SendARPProbe(myMAC, ip); err != nil {
			return err
		}
		logger.Debugf("sent an ARP probe for %v", ip)
	}

	return nil
}

func (r *processor) removeARPSender(deviceID string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	cancel, ok := r.canceller[deviceID]
	if !ok {
		return
	}
	cancel()
	delete(r.canceller, deviceID)
}

func (r *processor) OnPacketIn(finder network.Finder, ingress *network.Port, eth *protocol.Ethernet) error {
	// ARP?
	if eth.Type != 0x0806 {
		return r.BaseProcessor.OnPacketIn(finder, ingress, eth)
	}
	logger.Debugf("received ARP packet.. ingress=%v, srcEthMAC=%v, dstEthMAC=%v", ingress.ID(), eth.SrcMAC, eth.DstMAC)

	arp := new(protocol.ARP)
	if err := arp.UnmarshalBinary(eth.Payload); err != nil {
		return err
	}
	// ARP reply?
	if arp.Operation != 2 {
		// Do nothing.
		logger.Debugf("ignoring the ARP packet that is not a reply packet: ingress=%v, srcEthMAC=%v, dstEthMAC=%v", ingress.ID(), eth.SrcMAC, eth.DstMAC)
		return r.BaseProcessor.OnPacketIn(finder, ingress, eth)
	}

	// The source hardware address of this ARP reply packet should be equal to the myMAC address.
	if bytes.Equal(arp.SHA, myMAC) == false {
		logger.Warningf("unexpected ARP reply: %v", arp)
		// Drop this packet. Do not pass it to the next processors.
		return nil
	}

	swDPID, err := strconv.ParseUint(ingress.Device().ID(), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid device ID: %v", ingress.Device().ID())
	}

	// Update the host location in the database if THA and TPA are matched.
	updated, err := r.db.UpdateHostLocation(arp.THA, arp.TPA, swDPID, uint16(ingress.Number()))
	if err != nil {
		return err
	}
	// Remove installed flows for this host if the location has been changed.
	if updated {
		logger.Infof("host location updated: IP=%v, MAC=%v, deviceID=%v, portNum=%v", arp.TPA, arp.THA, swDPID, ingress.Number())
		// Remove flows from all devices.
		for _, device := range finder.Devices() {
			if err := device.RemoveFlowByMAC(arp.THA); err != nil {
				logger.Errorf("failed to remove flows from %v: %v", device.ID(), err)
				continue
			}
			logger.Infof("removed flows whose destination MAC address is %v on %v", arp.THA, device.ID())
		}
	}

	// This ARP reply packet has been processed. Do not pass it to the next processors.
	return nil
}

func (r *processor) OnPortUp(finder network.Finder, port *network.Port) error {
	if err := r.sendARPProbes(port.Device()); err != nil {
		logger.Errorf("failed to send ARP probes: %v", err)
		// Ignore this error and keep go on.
	}

	// Propagate this event to the next processors.
	return r.BaseProcessor.OnPortUp(finder, port)
}

func (r *processor) OnPortDown(finder network.Finder, port *network.Port) error {
	swDPID, err := strconv.ParseUint(port.Device().ID(), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid device ID: %v", port.Device().ID())
	}

	// Set NULLs to the host locations that associated with this port so that the
	// packets heading to these hosts will be broadcasted until we discover it again.
	if err := r.db.ResetHostLocationsByPort(swDPID, uint16(port.Number())); err != nil {
		return err
	}

	// Propagate this event to the next processors.
	return r.BaseProcessor.OnPortDown(finder, port)
}

func (r *processor) OnDeviceDown(finder network.Finder, device *network.Device) error {
	// Stop the ARP request sender.
	r.removeARPSender(device.ID())

	swDPID, err := strconv.ParseUint(device.ID(), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid device ID: %v", device.ID())
	}

	// Set NULLs to the host locations that belong to this device so that the packets
	// heading to these hosts will be broadcasted until we discover them again.
	if err := r.db.ResetHostLocationsByDevice(swDPID); err != nil {
		return err
	}

	// Propagate this event to the next processors.
	return r.BaseProcessor.OnDeviceDown(finder, device)
}