/*
 * Cherry - An OpenFlow Controller
 *
 * Copyright (C) 2015 Samjung Data Service Co., Ltd.,
 * Kitae Kim <superkkt@sds.co.kr>
 */

package of10

import (
	"encoding/binary"
	"git.sds.co.kr/cherry.git/cherryd/openflow"
)

func init() {
	openflow.RegisterParser(openflow.Ver10, ParseMessage)
}

func ParseMessage(data []byte) (openflow.Incoming, error) {
	msg := openflow.Message{}
	if err := msg.UnmarshalBinary(data); err != nil {
		return nil, err
	}

	var v openflow.Incoming

	switch msg.Type() {
	case OFPT_FEATURES_REPLY:
		v = new(FeaturesReply)
	case OFPT_GET_CONFIG_REPLY:
		v = new(GetConfigReply)
	case OFPT_STATS_REPLY:
		switch binary.BigEndian.Uint16(data[8:10]) {
		case OFPST_DESC:
			v = new(DescriptionReply)
		default:
			return nil, openflow.ErrUnsupportedMessage
		}
	case OFPT_PORT_STATUS:
		v = new(PortStatus)
	case OFPT_FLOW_REMOVED:
		v = new(FlowRemoved)
	default:
		return nil, openflow.ErrUnsupportedMessage
	}

	if err := v.UnmarshalBinary(data); err != nil {
		return nil, err
	}

	return v, nil
}
