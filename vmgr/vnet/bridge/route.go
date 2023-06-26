package bridge

import (
	"fmt"
	"math/bits"
	"net/netip"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

const (
	verboseDebug = false
)

func getRoutingTable() ([]route.Message, error) {
	// must check the entire routing table so we can ignore HOST routes
	// RTM_GET gives us any matching route, including host
	rib, err := route.FetchRIB(unix.AF_UNSPEC, unix.NET_RT_DUMP2, 0)
	if err != nil {
		return nil, fmt.Errorf("fetch rib: %w", err)
	}

	msgs, err := route.ParseRIB(unix.NET_RT_IFLIST2, rib)
	if err != nil {
		return nil, fmt.Errorf("parse rib: %w", err)
	}

	return msgs, nil
}

func HasValidRoute(routingTable []route.Message, targetSubnet netip.Prefix) (bool, error) {
	if verboseDebug {
		logMessages(routingTable)
	}

	for _, msg := range routingTable {
		msg, ok := msg.(*route.RouteMessage)
		if !ok {
			continue
		}

		// skip RTF_GLOBAL: default routes and internet routes in general
		// (no need to check netmask)
		if msg.Flags&unix.RTF_GLOBAL != 0 {
			continue
		}

		// skip specific hosts. we're looking for subnet routes
		// TODO: what about specific host routes added by VPNs? do we conflict? should we yield?
		//       maybe logic should be: if host is *in* our subnet and is not ours, yield
		if msg.Flags&unix.RTF_HOST != 0 {
			continue
		}

		// does it match our subnet?
		dstIP := ipOfAddr(addrType(msg.Addrs, unix.RTAX_DST))
		netmaskIP := ipOfAddr(addrType(msg.Addrs, unix.RTAX_NETMASK))
		if dstIP.IsValid() && netmaskIP.IsValid() {
			nbits := 0
			netmaskBytes := netmaskIP.AsSlice()
			for i := 0; i < len(netmaskBytes); i++ {
				nbits += bits.OnesCount8(netmaskBytes[i])
			}
			prefix, err := dstIP.Prefix(nbits)
			if err != nil {
				return false, fmt.Errorf("prefix: %w", err)
			}

			if prefix.Contains(targetSubnet.Addr()) {
				// found a route that matches our subnet/IP
				if verboseDebug {
					fmt.Println("consider matching route:", dstIP, netmaskIP, prefix, msg.Flags)
					logMessages([]route.Message{msg})
				}

				// if there is a route with Gateway flag, return true
				//    - Cisco AnyConnect and Zscaler have this set - they go to a gateway, not a link
				//    - ideally, if VPNs are Network Extensions, vmnet will return VMNET_GENERAL_FAILURE on conflict. but not if VPNs don't use NE (like AnyConnect and Zscaler)
				//      WireGuard uses NE, so it's OK
				if msg.Flags&unix.RTF_GATEWAY != 0 {
					if verboseDebug {
						fmt.Println("found gateway")
					}
					return true, nil
				}

				// if there is a route WITHOUT Static flag, return true
				//    - this means it's a link or implicit route from adding an interface
				//    - regardless of whether the interface is ours or not, this means an interface is using the same subnet,
				//      so let's not fight with it. we're trying to be the more passive one
				//    - Tailscale sets Static flag
				if msg.Flags&unix.RTF_STATIC == 0 {
					if verboseDebug {
						fmt.Println("found no static")
					}
					return true, nil
				}

				// if the IFA (source IP of the route/interface) is ours, return true
				//    - this means it must be our route
				// unncessary check because of Static flag
				// since we don't care about IFA, we can use starting IP of subnet instead of last IP
				// should be more reliable for finding conflicts at start of subnet
				/*
					ifaIP := ipOfAddr(addrType(msg.Addrs, unix.RTAX_IFA))
					if ifaIP.IsValid() && ifaIP == hostIP {
						if verboseDebug {
							fmt.Println("found ifa")
						}
						return true, nil
					}
				*/
			}
		}
	}

	// if no route found, return false
	if verboseDebug {
		fmt.Println("no route found")
	}
	return false, nil
}

func HasAnyValidRoutes(routingTable []route.Message, targetSubnet4, targetSubnet6 netip.Prefix) (bool, error) {
	// look up routing table once, if needed
	if routingTable == nil {
		var err error
		routingTable, err = getRoutingTable()
		if err != nil {
			return false, fmt.Errorf("get routing table: %w", err)
		}
	}

	if targetSubnet4.IsValid() {
		hasRoute, err := HasValidRoute(routingTable, targetSubnet4)
		if err != nil {
			return false, fmt.Errorf("route4: %w", err)
		}

		if hasRoute {
			return true, nil
		}
	}

	if targetSubnet6.IsValid() {
		hasRoute, err := HasValidRoute(routingTable, targetSubnet6)
		if err != nil {
			return false, fmt.Errorf("route6: %w", err)
		}

		if hasRoute {
			return true, nil
		}
	}

	return false, nil
}

// addrType returns addrs[rtaxType], if that (the route address type) exists,
// else it returns nil.
//
// The RTAX_* constants at https://github.com/apple/darwin-xnu/blob/main/bsd/net/route.h
// for what each address index represents.
func addrType(addrs []route.Addr, rtaxType int) route.Addr {
	if len(addrs) > rtaxType {
		return addrs[rtaxType]
	}
	return nil
}

// ipOfAddr returns the route.Addr (possibly nil) as a netip.Addr
// (possibly zero).
func ipOfAddr(a route.Addr) netip.Addr {
	switch a := a.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		ip := netip.AddrFrom16(a.IP)
		if a.ZoneID != 0 {
			ip = ip.WithZone(fmt.Sprint(a.ZoneID)) // TODO: look up net.InterfaceByIndex? but it might be changing?
		}
		return ip
	}
	return netip.Addr{}
}
