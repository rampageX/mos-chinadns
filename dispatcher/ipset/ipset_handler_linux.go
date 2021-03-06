// +build linux

//     Copyright (C) 2020, IrineSistiana
//
//     This file is part of mos-chinadns.
//
//     mos-chinadns is free software: you can redistribute it and/or modify
//     it under the terms of the GNU General Public License as published by
//     the Free Software Foundation, either version 3 of the License, or
//     (at your option) any later version.
//
//     mos-chinadns is distributed in the hope that it will be useful,
//     but WITHOUT ANY WARRANTY; without even the implied warranty of
//     MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//     GNU General Public License for more details.
//
//     You should have received a copy of the GNU General Public License
//     along with this program.  If not, see <https://www.gnu.org/licenses/>.

package ipset

import (
	"fmt"
	"github.com/IrineSistiana/mos-chinadns/dispatcher/config"
	"github.com/IrineSistiana/mos-chinadns/dispatcher/logger"
	"github.com/IrineSistiana/mos-chinadns/dispatcher/policy"
	"github.com/miekg/dns"
)

type Handler struct {
	checkCAME    bool
	mask4, mask6 uint8
	rules        []*rule
}

type rule struct {
	setName4       string
	setName6       string
	domainPolicies *policy.DomainPolicies
}

func NewIPSetHandler(c *config.Config) (*Handler, error) {
	h := new(Handler)
	h.checkCAME = c.IPSet.CheckCNAME
	h.mask4 = c.IPSet.Mask4
	h.mask6 = c.IPSet.Mask6

	// default
	if h.mask4 == 0 {
		h.mask4 = 24
	}
	if h.mask6 == 0 {
		h.mask6 = 32
	}

	for _, ipsetConfig := range c.IPSet.Rule {
		if len(ipsetConfig.SetName4) == 0 && len(ipsetConfig.SetName6) == 0 {
			continue
		}

		dps, err := policy.NewDomainPolicies(ipsetConfig.Domain, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to init ipset domain policies %s: %w", ipsetConfig.Domain, err)
		}
		rule := &rule{
			setName4:       ipsetConfig.SetName4,
			setName6:       ipsetConfig.SetName6,
			domainPolicies: dps,
		}

		h.rules = append(h.rules, rule)
	}
	return h, nil
}

func (h *Handler) ApplyIPSet(q, r *dns.Msg) error {
	for _, rule := range h.rules {
		domainMatched := false

		for i := range q.Question { // match question first
			if action := rule.domainPolicies.Match(q.Question[i].Name); action != nil && action.Mode == policy.PolicyActionAccept {
				domainMatched = true
				break
			}
		}
		if !domainMatched && h.checkCAME { // match cname
			for i := range r.Answer {
				if cname, ok := r.Answer[i].(*dns.CNAME); ok {
					if action := rule.domainPolicies.Match(cname.Target); action != nil && action.Mode == policy.PolicyActionAccept {
						domainMatched = true
						break
					}
				}
			}
		}

		if domainMatched {
			for i := range r.Answer {
				entry := new(Entry)

				switch rr := r.Answer[i].(type) {
				case *dns.A:
					entry.IP = rr.A
					entry.SetName = rule.setName4
					entry.Mask = h.mask4
					entry.IsNET6 = false
				case *dns.AAAA:
					entry.IP = rr.AAAA
					entry.SetName = rule.setName6
					entry.Mask = h.mask6
					entry.IsNET6 = true
				default:
					continue
				}

				if len(entry.SetName) == 0 {
					continue
				}

				logger.GetStd().Debugf("ApplyIPSet: [%v %d]: add %s/%d to set %s", q.Question, q.Id, entry.IP, entry.Mask, entry.SetName)
				err := AddCIDR(entry)
				if err != nil {
					return fmt.Errorf("failed to add ip %s to set %s: %w", entry.IP, entry.SetName, err)
				}
			}
		}
	}

	return nil
}
