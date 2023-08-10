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
	if ((ip6 + 1) > data_end) {
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

	// Calculate the IPv4 one's complement checksum of the IPv4 header.
	__wsum sum4 = 0;

	for (int i = 0; i < sizeof(ip) / sizeof(__u16); ++i)
		sum4 += ((__u16 *)&ip)[i];

	// Note that sum4 is guaranteed to be non-zero by virtue of ip.version == 4
	sum4 = (sum4 & 0xFFFF) + (sum4 >> 16);  // collapse u32 into range 1 .. 0x1FFFE
	sum4 = (sum4 & 0xFFFF) + (sum4 >> 16);  // collapse any potential carry into u16
	ip.check = (__u16)~sum4;                // sum4 cannot be zero, so this is never 0xFFFF

	// Calculate the *negative* IPv6 16-bit one's complement checksum of the IPv6 header.
	__wsum sum6 = 0;
	// We'll end up with a non-zero sum due to ip6->version == 6 (which has '0' bits)
	for (int i = 0; i < sizeof(*ip6) / sizeof(__u16); ++i)
		sum6 += ~((__u16 *)ip6)[i];  // note the bitwise negation

	// Note that there is no L4 checksum update: we are relying on the checksum neutrality
	// of the ipv6 address chosen by netd's ClatdController.

	// Packet mutations begin - point of no return, but if this first modification fails
	// the packet is probably still pristine, so let clatd handle it.
	if (bpf_skb_change_proto(skb, bpf_htons(ETH_P_IP), 0)) {
		bpf_printk("change proto failed\n");
		return TC_ACT_SHOT;
	}
	bpf_csum_update(skb, sum6);

	data = (void *)(long)skb->data;
	data_end = (void *)(long)skb->data_end;
	eth = data;
	if (data + sizeof(*eth) + sizeof(struct iphdr) > data_end) {
		bpf_printk("no ip header\n");
		return TC_ACT_SHOT;
	}

    // write new headers
	eth->h_proto = bpf_htons(ETH_P_IP);
	*(struct iphdr *)(eth + 1) = ip;

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
	if ((ip4 + 1) > data_end)
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

	// Calculate the IPv4 one's complement checksum of the IPv4 header.
	__wsum sum4 = 0;

	for (int i = 0; i < sizeof(*ip4) / sizeof(__u16); ++i)
		sum4 += ((__u16 *)ip4)[i];

	// Note that sum4 is guaranteed to be non-zero by virtue of ip4->version == 4
	sum4 = (sum4 & 0xFFFF) + (sum4 >> 16);  // collapse u32 into range 1 .. 0x1FFFE
	sum4 = (sum4 & 0xFFFF) + (sum4 >> 16);  // collapse any potential carry into u16
	// for a correct checksum we should get *a* zero, but sum4 must be positive, ie 0xFFFF
	if (sum4 != 0xFFFF)
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

	// Calculate the IPv6 16-bit one's complement checksum of the IPv6 header.
	__wsum sum6 = 0;
	// We'll end up with a non-zero sum due to ip6.version == 6
	for (int i = 0; i < sizeof(ip6) / sizeof(__u16); ++i)
		sum6 += ((__u16 *)&ip6)[i];

	// Packet mutations begin - point of no return, but if this first modification fails
	// the packet is probably still pristine, so let clatd handle it.
	if (bpf_skb_change_proto(skb, bpf_htons(ETH_P_IPV6), 0))
		return TC_ACT_SHOT;

	// This takes care of updating the skb->csum field for a CHECKSUM_COMPLETE packet.
	// In such a case, skb->csum is a 16-bit one's complement sum of the entire payload,
	// thus we need to subtract out the ipv4 header's sum, and add in the ipv6 header's sum.
	// However, we've already verified the ipv4 checksum is correct and thus 0.
	// Thus we only need to add the ipv6 header's sum.
	//
	// bpf_csum_update() always succeeds if the skb is CHECKSUM_COMPLETE and returns an error
	// (-ENOTSUPP) if it isn't.  So we just ignore the return code (see above for more details).
	bpf_csum_update(skb, sum6);

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
char _license[] SEC("license") = "Apache 2.0 + Proprietary";
#else
char _license[] SEC("license") = "GPL";
#endif
