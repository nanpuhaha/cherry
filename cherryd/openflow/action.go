/*
 * Cherry - An OpenFlow Controller
 *
 * Copyright (C) 2015 Samjung Data Service Co., Ltd.,
 * Kitae Kim <superkkt@sds.co.kr>
 */

package openflow

import (
	"encoding"
	"errors"
	"net"
)

const (
	PortTable      uint = 0xfffffff9
	PortAll             = 0xfffffffc
	PortController      = 0xfffffffd
	PortAny             = 0xffffffff // PortNone
)

type Action interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
	// TODO: Implement multiple output actions
	SetOutput(port uint) error
	Output() []uint
	SetSrcMAC(mac net.HardwareAddr) error
	SrcMAC() (ok bool, mac net.HardwareAddr)
	SetDstMAC(mac net.HardwareAddr) error
	DstMAC() (ok bool, mac net.HardwareAddr)
}

type BaseAction struct {
	output map[uint]interface{}
	srcMAC *net.HardwareAddr
	dstMAC *net.HardwareAddr
}

func NewBaseAction() *BaseAction {
	return &BaseAction{
		output: make(map[uint]interface{}),
	}
}

func (r *BaseAction) SetOutput(port uint) error {
	r.output[port] = nil
	return nil
}

func (r *BaseAction) Output() []uint {
	ports := make([]uint, 0)
	for v, _ := range r.output {
		ports = append(ports, v)
	}

	return ports
}

func (r *BaseAction) SetSrcMAC(mac net.HardwareAddr) error {
	if mac == nil || len(mac) < 6 {
		return errors.New("invalid MAC address")
	}

	r.srcMAC = &mac
	return nil
}

func (r *BaseAction) SrcMAC() (ok bool, mac net.HardwareAddr) {
	if r.srcMAC == nil {
		return false, ZeroMAC
	}

	return true, *r.srcMAC
}

func (r *BaseAction) SetDstMAC(mac net.HardwareAddr) error {
	if mac == nil || len(mac) < 6 {
		return errors.New("invalid MAC address")
	}

	r.dstMAC = &mac
	return nil
}

func (r *BaseAction) DstMAC() (ok bool, mac net.HardwareAddr) {
	if r.dstMAC == nil {
		return false, ZeroMAC
	}

	return true, *r.dstMAC
}
