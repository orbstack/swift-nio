// Copyright 2023 Orbital Labs, LLC
// License: Apache 2.0 for base. Changes proprietary and confidential.

// based on AOSP clatd
/*
 * Copyright (C) 2019 The Android Open Source Project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// NAT64 for IPv4 vlan bridge access from macOS host.

#include <string.h>
#include <stdbool.h>

#include <linux/stddef.h>
#include <linux/bpf.h>
#include <linux/if.h>
#include <linux/if_ether.h>
#include <linux/if_packet.h>
#include <linux/in.h>
#include <linux/in6.h>
#include <linux/ip.h>
#include <linux/ipv6.h>
#include <linux/pkt_cls.h>
#include <linux/swab.h>
#include <linux/udp.h>
#include <linux/icmp.h>
#include <linux/icmpv6.h>
#include <errno.h>
#include <time.h>

#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// warning: this makes it GPL
//#define DEBUG

#ifndef DEBUG
#ifdef bpf_printk
#undef bpf_printk
#endif
#define bpf_printk(fmt, ...) do { } while (0)
#endif

#define IP4(a, b, c, d) (bpf_htonl((a << 24) | (b << 16) | (c << 8) | d))
#define IP6(a,b,c,d,e,f,g,h) {bpf_htonl(a << 16 | b), bpf_htonl(c << 16 | d), bpf_htonl(e << 16 | f), bpf_htonl(g << 16 | h)}

#define copy4(dst, src) \
    dst[0] = src[0]; \
    dst[1] = src[1]; \
    dst[2] = src[2]; \
    dst[3] = src[3];

// falls under scon machine /64
// (always leave standard form of IP here for searching if we change it later)
// chosen to be checksum-neutral for stateless NAT64 w/o L4 (TCP/UDP) checksum update: this prefix adds up to 0
// fd07:b51a:cc66:0:a617:db5e/96
static const __be32 XLAT_PREFIX6[4] = IP6(0xfd07, 0xb51a, 0xcc66, 0x0000, 0xa617, 0xdb5e, 0x0000, 0x0000);

// source ip after translation
// we use this ip, outside of machine bridge, so that docker machine routes the reply via default route (i.e. us)
// then we use a static ip route to redirect it to eth1 (where our egress4 prog is attached)
//
// ideally we'd MASQUERADE this to Docker bridge gateway IP in Docker machine, but we need to make sure cfwd can differentiate it and make sk_lookup hook kick in
// I tried to mod the kernel and add mark to "struct bpf_sk_lookup" but it doesn't work b/c skb is usually NULL at the lookup point.
// so instead, use a weird random private IP to make private RFC IP checks like Keycloak happy, while minimizing chance of conflict
// marks are lost across bridge too, but i'm sure there's a way around that
//
// TODO do this better by figuring out some other way to mark and let cfwd know, e.g. bpf maps
// really not ideal to let Docker containers/servers see this weird IP
//
// 10.183.233.241
#define NAT64_SRC_IP4 IP4(10, 183, 233, 241)

// source IP of incoming 6->4 packets
// dest IP of outgoing 4->6
// this is the xlat-mapped version of NAT64_SRC_IP4 source addr. that way, we get full checksum neutrality
// fd07:b51a:cc66:0:a617:db5e:0ab7:e9f1
static const __be32 XLAT_SRC_IP6[4] = IP6(0xfd07, 0xb51a, 0xcc66, 0x0000, 0xa617, 0xdb5e, 0x0ab7, 0xe9f1);

// da:9b:d0:54:e0:02
static const __u8 BRIDGE_GUEST_MAC[ETH_ALEN] = "\xda\x9b\xd0\x54\xe0\x02";

#define MARK_NAT64 0xe97bd031

#define IP_DF 0x4000  // Flag: "Don't Fragment"

/* SPDX-License-Identifier: BSD-2-Clause */
/* Copyright Authors of Cilium */
static __always_inline __wsum csum_add(__wsum csum, __wsum addend)
{
	csum += addend;
	return csum + (csum < addend);
}

static __always_inline __wsum csum_sub(__wsum csum, __wsum addend)
{
	return csum_add(csum, ~addend);
}

static __always_inline __be32 ipv6_pseudohdr_checksum(struct ipv6hdr *hdr,
						      __u8 next_hdr,
						      __u16 payload_len, __be32 sum)
{
	__be32 len = bpf_htonl((__u32)payload_len);
	__be32 nexthdr = bpf_htonl((__u32)next_hdr);

	sum = bpf_csum_diff(NULL, 0, (__be32 *)&hdr->saddr, sizeof(struct in6_addr), sum);
	sum = bpf_csum_diff(NULL, 0, (__be32 *)&hdr->daddr, sizeof(struct in6_addr), sum);
	sum = bpf_csum_diff(NULL, 0, (__be32 *)&len, sizeof(len), sum);
	sum = bpf_csum_diff(NULL, 0, (__be32 *)&nexthdr, sizeof(nexthdr), sum);

	return sum;
}

static int icmp4_to_icmp6(struct __sk_buff *skb, int nh_off) {
	struct icmphdr icmp4;
	struct icmp6hdr icmp6 = {};

	if (bpf_skb_load_bytes(skb, nh_off, &icmp4, sizeof(icmp4)) < 0)
		return -1;
	icmp6.icmp6_cksum = icmp4.checksum;
	switch (icmp4.type) {
	case ICMP_ECHO:
		icmp6.icmp6_type = ICMPV6_ECHO_REQUEST;
		icmp6.icmp6_identifier = icmp4.un.echo.id;
		icmp6.icmp6_sequence = icmp4.un.echo.sequence;
		break;
	case ICMP_ECHOREPLY:
		icmp6.icmp6_type = ICMPV6_ECHO_REPLY;
		icmp6.icmp6_identifier = icmp4.un.echo.id;
		icmp6.icmp6_sequence = icmp4.un.echo.sequence;
		break;
	case ICMP_DEST_UNREACH:
		icmp6.icmp6_type = ICMPV6_DEST_UNREACH;
		switch (icmp4.code) {
		case ICMP_NET_UNREACH:
		case ICMP_HOST_UNREACH:
			icmp6.icmp6_code = ICMPV6_NOROUTE;
			break;
		case ICMP_PROT_UNREACH:
			icmp6.icmp6_type = ICMPV6_PARAMPROB;
			icmp6.icmp6_code = ICMPV6_UNK_NEXTHDR;
			icmp6.icmp6_pointer = 6;
			break;
		case ICMP_PORT_UNREACH:
			icmp6.icmp6_code = ICMPV6_PORT_UNREACH;
			break;
		case ICMP_FRAG_NEEDED:
			icmp6.icmp6_type = ICMPV6_PKT_TOOBIG;
			icmp6.icmp6_code = 0;
			/* FIXME */
			if (icmp4.un.frag.mtu)
				icmp6.icmp6_mtu = bpf_htonl(bpf_ntohs(icmp4.un.frag.mtu));
			else
				icmp6.icmp6_mtu = bpf_htonl(1500);
			break;
		case ICMP_SR_FAILED:
			icmp6.icmp6_code = ICMPV6_NOROUTE;
			break;
		case ICMP_NET_UNKNOWN:
		case ICMP_HOST_UNKNOWN:
		case ICMP_HOST_ISOLATED:
		case ICMP_NET_UNR_TOS:
		case ICMP_HOST_UNR_TOS:
			icmp6.icmp6_code = 0;
			break;
		case ICMP_NET_ANO:
		case ICMP_HOST_ANO:
		case ICMP_PKT_FILTERED:
			icmp6.icmp6_code = ICMPV6_ADM_PROHIBITED;
			break;
		default:
			return -1;
		}
		break;
	case ICMP_TIME_EXCEEDED:
		icmp6.icmp6_type = ICMPV6_TIME_EXCEED;
		break;
	case ICMP_PARAMETERPROB:
		icmp6.icmp6_type = ICMPV6_PARAMPROB;
		/* FIXME */
		icmp6.icmp6_pointer = 6;
		break;
	default:
		return -1;
	}
	if (bpf_skb_store_bytes(skb, nh_off, &icmp6, sizeof(icmp6), 0) < 0)
		return -1;
	icmp4.checksum = 0;
	icmp6.icmp6_cksum = 0;
	return bpf_csum_diff((__be32 *)&icmp4, sizeof(icmp4), (__be32 *)&icmp6, sizeof(icmp6), 0);
}

static int icmp6_to_icmp4(struct __sk_buff *skb, int nh_off) {
	struct icmphdr icmp4 = {};
	struct icmp6hdr icmp6;
	__u32 mtu;

	if (bpf_skb_load_bytes(skb, nh_off, &icmp6, sizeof(icmp6)) < 0)
		return -1;
	icmp4.checksum = icmp6.icmp6_cksum;
	switch (icmp6.icmp6_type) {
	case ICMPV6_ECHO_REQUEST:
		icmp4.type = ICMP_ECHO;
		icmp4.un.echo.id = icmp6.icmp6_identifier;
		icmp4.un.echo.sequence = icmp6.icmp6_sequence;
		break;
	case ICMPV6_ECHO_REPLY:
		icmp4.type = ICMP_ECHOREPLY;
		icmp4.un.echo.id = icmp6.icmp6_identifier;
		icmp4.un.echo.sequence = icmp6.icmp6_sequence;
		break;
	case ICMPV6_DEST_UNREACH:
		icmp4.type = ICMP_DEST_UNREACH;
		switch (icmp6.icmp6_code) {
		case ICMPV6_NOROUTE:
		case ICMPV6_NOT_NEIGHBOUR:
		case ICMPV6_ADDR_UNREACH:
			icmp4.code = ICMP_HOST_UNREACH;
			break;
		case ICMPV6_ADM_PROHIBITED:
			icmp4.code = ICMP_HOST_ANO;
			break;
		case ICMPV6_PORT_UNREACH:
			icmp4.code = ICMP_PORT_UNREACH;
			break;
		default:
			return -1;
		}
		break;
	case ICMPV6_PKT_TOOBIG:
		icmp4.type = ICMP_DEST_UNREACH;
		icmp4.code = ICMP_FRAG_NEEDED;
		/* FIXME */
		if (icmp6.icmp6_mtu) {
			mtu = bpf_ntohl(icmp6.icmp6_mtu);
			icmp4.un.frag.mtu = bpf_htons((__u16)mtu);
		} else {
			icmp4.un.frag.mtu = bpf_htons(1500);
		}
		break;
	case ICMPV6_TIME_EXCEED:
		icmp4.type = ICMP_TIME_EXCEEDED;
		icmp4.code = icmp6.icmp6_code;
		break;
	case ICMPV6_PARAMPROB:
		switch (icmp6.icmp6_code) {
		case ICMPV6_HDR_FIELD:
			icmp4.type = ICMP_PARAMETERPROB;
			icmp4.code = 0;
			break;
		case ICMPV6_UNK_NEXTHDR:
			icmp4.type = ICMP_DEST_UNREACH;
			icmp4.code = ICMP_PROT_UNREACH;
			break;
		default:
			return -1;
		}
		break;
	default:
		return -1;
	}
	if (bpf_skb_store_bytes(skb, nh_off, &icmp4, sizeof(icmp4), 0) < 0)
		return -1;
	icmp4.checksum = 0;
	icmp6.icmp6_cksum = 0;
	return bpf_csum_diff((__be32 *)&icmp6, sizeof(icmp6), (__be32 *)&icmp4, sizeof(icmp4), 0);
}

// TC_ACT_PIPE means to continue with the next filter, if any

SEC("tc")
int sched_cls_ingress6_nat6(struct __sk_buff *skb) {
	void *data = (void *)(long)skb->data;
	const void *data_end = (void *)(long)skb->data_end;
	struct ethhdr *eth = data;
	struct ipv6hdr *ip6 = (void *)(eth + 1);

	// Require ethernet dst mac address to be our unicast address.
	if (skb->pkt_type != PACKET_HOST) {
		bpf_printk("not host\n");
		return TC_ACT_PIPE;
	}

	// Must have (ethernet and) ipv6 header
	if ((void *)(ip6 + 1) > data_end) {
		bpf_printk("no ipv6 header\n");
		return TC_ACT_PIPE;
	}

    // check dest subnet /96
	// do this early so we exit fast for pure v6 traffic
    if (ip6->daddr.in6_u.u6_addr32[0] != XLAT_PREFIX6[0] ||
        ip6->daddr.in6_u.u6_addr32[1] != XLAT_PREFIX6[1] ||
        ip6->daddr.in6_u.u6_addr32[2] != XLAT_PREFIX6[2]) {
		bpf_printk("not in subnet\n");
        return TC_ACT_PIPE;
    }

	// drop packet on any failure after subnet check. it'll be wrong anyway

	// IP version must be 6
	if (ip6->version != 6) {
		bpf_printk("not ipv6\n");
		return TC_ACT_SHOT;
	}

	// Maximum IPv6 payload length that can be translated to IPv4
	if (bpf_ntohs(ip6->payload_len) > 0xFFFF - sizeof(struct iphdr)) {
		bpf_printk("payload too big\n");
		return TC_ACT_SHOT;
	}
	switch (ip6->nexthdr) {
	case IPPROTO_TCP:  // For TCP & UDP the checksum neutrality of the chosen IPv6
	case IPPROTO_UDP:  // address means there is no need to update their checksums.
	case IPPROTO_GRE:  // We do not need to bother looking at GRE/ESP headers,
	case IPPROTO_ESP:  // since there is never a checksum to update.
	case IPPROTO_ICMPV6:
		break;
	default:  // do not know how to handle anything else
		bpf_printk("not tcp/udp/gre/esp\n");
		return TC_ACT_SHOT;
	}

    // begin translation

	struct iphdr ip = {
		.version = 4,                                                      // u4
		.ihl = sizeof(struct iphdr) / sizeof(__u32),                       // u4
		.tos = (ip6->priority << 4) + (ip6->flow_lbl[0] >> 4),             // u8
		.tot_len = bpf_htons(bpf_ntohs(ip6->payload_len) + sizeof(struct iphdr)),  // u16
		.id = 0,                                                           // u16
		.frag_off = bpf_htons(IP_DF),                                          // u16
		.ttl = ip6->hop_limit,                                             // u8
		.protocol = ip6->nexthdr,                                          // u8
		.check = 0,                                                        // u16
		.saddr = NAT64_SRC_IP4,
		.daddr = ip6->daddr.in6_u.u6_addr32[3],
	};

	// icmpv6 -> v4
	int l4csum = 0;
	if (ip6->nexthdr == IPPROTO_ICMPV6) {
		ip.protocol = IPPROTO_ICMP;

		// ICMPv4 has no L4 checksum
		l4csum = icmp6_to_icmp4(skb, ETH_HLEN + sizeof(struct ipv6hdr));
		if (l4csum < 0) {
			bpf_printk("icmp6_to_icmp4 failed\n");
			return TC_ACT_SHOT;
		}

		// reload pointers
		// TODO switch to direct packet access
		data = (void *)(long)skb->data;
		data_end = (void *)(long)skb->data_end;
		eth = data;
		ip6 = (void *)(eth + 1);
		if ((void *)(ip6 + 1) > data_end) {
			bpf_printk("no ipv6 header\n");
			return TC_ACT_PIPE;
		}

		__be32 csum1 = ipv6_pseudohdr_checksum(ip6, IPPROTO_ICMPV6,
						bpf_ntohs(ip6->payload_len), 0);
		l4csum = csum_sub(l4csum, csum1);
	}

	// Calculate the IPv4 one's complement checksum of the IPv4 header.
	// csum_diff is NOT a 16-bit checksum! it's an opaque 32-bit value
	// differs by arch. `ip.check = ~diff` works on arm64 but not x86
	__wsum diff = bpf_csum_diff(NULL, 0, (__be32 *)&ip, sizeof(ip), 0);

	// Note that there is no L4 checksum update: we are relying on the checksum neutrality
	// of the ipv6 address chosen by netd's ClatdController.

	// Packet mutations begin - point of no return, but if this first modification fails
	// the packet is probably still pristine, so let clatd handle it.
	if (bpf_skb_change_proto(skb, bpf_htons(ETH_P_IP), 0)) {
		bpf_printk("change proto failed\n");
		return TC_ACT_SHOT;
	}

	data = (void *)(long)skb->data;
	data_end = (void *)(long)skb->data_end;
	eth = data;
	if (data + ETH_HLEN + sizeof(struct iphdr) > data_end) {
		bpf_printk("no ip header\n");
		return TC_ACT_SHOT;
	}

    // write new headers
	eth->h_proto = bpf_htons(ETH_P_IP);
	*(struct iphdr *)(eth + 1) = ip;

	// set after writing iphdr, otherwise it'll be overwritten by check=0
	if (bpf_l3_csum_replace(skb, ETH_HLEN + offsetof(struct iphdr, check), 0, diff, 0)) {
		bpf_printk("ipv4 csum failed\n");
		return TC_ACT_SHOT;
	}
	if (ip.protocol == IPPROTO_ICMP) {
		if (bpf_l4_csum_replace(skb, ETH_HLEN + sizeof(ip) + offsetof(struct icmphdr, checksum), 0, l4csum, BPF_F_PSEUDO_HDR)) {
			bpf_printk("icmpv4 csum failed\n");
			return TC_ACT_SHOT;
		}
	}

    // mark and re-inject
    skb->mark = MARK_NAT64; // route to docker machine (via ip rule)
	return bpf_redirect(skb->ifindex, BPF_F_INGRESS);
}

// no address checking in this path. non-translated packet can't get here b/c routing
SEC("tc")
int sched_cls_egress4_nat4(struct __sk_buff *skb) {
	void *data = (void *)(long)skb->data;
	void *data_end = (void *)(long)skb->data_end;
	struct ethhdr *eth = data;
	struct iphdr *ip4 = (void *)(eth + 1);

	// Require ethernet dst mac address to be our unicast address.
	if (skb->pkt_type != PACKET_HOST)
		return TC_ACT_PIPE;

	// Must have ipv4 header
	if ((void *)(ip4 + 1) > data_end)
		return TC_ACT_PIPE;

	// only if translated
	// do this early so we exit fast for pure v4 traffic
	if (ip4->daddr != NAT64_SRC_IP4)
		return TC_ACT_PIPE;

	// drop packet on any failure after subnet check. it'll be wrong anyway

	// IP version must be 4
	if (ip4->version != 4)
		return TC_ACT_SHOT;

	// We cannot handle IP options, just standard 20 byte == 5 dword minimal IPv4 header
	if (ip4->ihl != 5)
		return TC_ACT_SHOT;

	// Maximum IPv6 payload length that can be translated to IPv4
	if (bpf_htons(ip4->tot_len) > 0xFFFF - sizeof(struct iphdr))
		return TC_ACT_SHOT;

	// Minimum IPv4 total length is the size of the header
	if (bpf_ntohs(ip4->tot_len) < sizeof(*ip4))
		return TC_ACT_SHOT;

	// We are incapable of dealing with IPv4 fragments
	if (ip4->frag_off & ~bpf_htons(IP_DF))
		return TC_ACT_SHOT;

    // begin modification

	switch (ip4->protocol) {
	case IPPROTO_TCP:  // For TCP & UDP the checksum neutrality of the chosen IPv6
	case IPPROTO_GRE:  // address means there is no need to update their checksums.
	case IPPROTO_ESP:  // We do not need to bother looking at GRE/ESP headers,
	case IPPROTO_ICMP:
		break;         // since there is never a checksum to update.

	case IPPROTO_UDP:  // See above comment, but must also have UDP header...
		if (data + ETH_HLEN + sizeof(*ip4) + sizeof(struct udphdr) > data_end)
			return TC_ACT_SHOT;
        // TODO: fix checksum properly. 0 is invalid for IPv6 but we use csum offload
		struct udphdr *uh = (struct udphdr *)(ip4 + 1);
		if (uh->check == 0) {
            uh->check = 0xffff;
        }
		break;

	default:  // do not know how to handle anything else
		return TC_ACT_SHOT;
	}

	struct ipv6hdr ip6 = {
		.version = 6,                                    // __u8:4
		.priority = ip4->tos >> 4,                       // __u8:4
		.flow_lbl = {(ip4->tos & 0xF) << 4, 0, 0},       // __u8[3]
		.payload_len = bpf_htons(bpf_ntohs(ip4->tot_len) - 20),  // __be16
		.nexthdr = ip4->protocol,                        // __u8
		.hop_limit = ip4->ttl,                           // __u8
	};
	copy4(ip6.saddr.in6_u.u6_addr32, XLAT_PREFIX6);
    ip6.saddr.in6_u.u6_addr32[3] = ip4->saddr;
	copy4(ip6.daddr.in6_u.u6_addr32, XLAT_SRC_IP6);

	// icmpv4 -> v6
	int l4csum = 0;
	if (ip4->protocol == IPPROTO_ICMP) {
		ip6.nexthdr = IPPROTO_ICMPV6;
		l4csum = icmp4_to_icmp6(skb, ETH_HLEN + sizeof(*ip4));

		// reload pointers
		// TODO switch to direct packet access
		data = (void *)(long)skb->data;
		data_end = (void *)(long)skb->data_end;
		eth = data;
		ip4 = (void *)(eth + 1);
		if ((void *)(ip4 + 1) > data_end) {
			bpf_printk("no ipv4 header\n");
			return TC_ACT_PIPE;
		}

		__be32 csum1 = ipv6_pseudohdr_checksum(&ip6, IPPROTO_ICMPV6,
						bpf_ntohs(ip6.payload_len), 0);
		l4csum = csum_add(l4csum, csum1);
		if (bpf_l4_csum_replace(skb, ETH_HLEN + sizeof(*ip4) + offsetof(struct icmp6hdr, icmp6_cksum), 0, l4csum, BPF_F_PSEUDO_HDR)) {
			bpf_printk("icmpv6 csum failed\n");
			return TC_ACT_SHOT;
		}
	}

	// Packet mutations begin - point of no return, but if this first modification fails
	// the packet is probably still pristine, so let clatd handle it.
	if (bpf_skb_change_proto(skb, bpf_htons(ETH_P_IPV6), 0))
		return TC_ACT_SHOT;

	// bpf_skb_change_proto() invalidates all pointers - reload them.
	data = (void *)(long)skb->data;
	data_end = (void *)(long)skb->data_end;
	eth = data;
	if (data + ETH_HLEN + sizeof(ip6) > data_end)
		return TC_ACT_SHOT;

	// update headers
	eth->h_proto = bpf_htons(ETH_P_IPV6);
	*(struct ipv6hdr *)(eth + 1) = ip6;
	return TC_ACT_PIPE;
}

#ifndef DEBUG
char _license[] SEC("license") = "Apache 2.0 + BSD 2-Clause + Proprietary";
#else
char _license[] SEC("license") = "GPL";
#endif
