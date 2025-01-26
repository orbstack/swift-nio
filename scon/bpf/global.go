package bpf

import (
	"errors"
	"fmt"
	"io"

	"github.com/cilium/ebpf/link"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type GlobalBpfManager struct {
	closers []io.Closer

	bnatObjs     *bnatObjects
	bnatQdisc    *netlink.GenericQdisc
	bnatIngress6 *netlink.BpfFilter
	bnatEgress4  *netlink.BpfFilter
}

func NewGlobalBpfManager() (*GlobalBpfManager, error) {
	return &GlobalBpfManager{}, nil
}

func (b *GlobalBpfManager) Close() error {
	if b.bnatIngress6 != nil {
		_ = netlink.FilterDel(b.bnatIngress6)
	}
	if b.bnatEgress4 != nil {
		_ = netlink.FilterDel(b.bnatEgress4)
	}
	if b.bnatQdisc != nil {
		_ = netlink.QdiscDel(b.bnatQdisc)
	}

	var errs []error
	for _, c := range b.closers {
		err := c.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *GlobalBpfManager) Load(ifVmnetMachine string) error {
	bnatSpec, err := loadBnat()
	if err != nil {
		return fmt.Errorf("load bnat: %w", err)
	}

	bnatObjs := &bnatObjects{}
	err = bnatSpec.LoadAndAssign(bnatObjs, nil)
	if err != nil {
		return fmt.Errorf("load and assign: %w", err)
	}
	b.closers = append(b.closers, bnatObjs)
	b.bnatObjs = bnatObjs

	// add clsact qdisc to eth1
	iface, err := netlink.LinkByName(ifVmnetMachine)
	if err != nil {
		return fmt.Errorf("get iface: %w", err)
	}
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: iface.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}
	err = netlink.QdiscAdd(qdisc)
	if err != nil && errors.Is(err, unix.EEXIST) {
		_ = netlink.QdiscDel(qdisc)
		err = netlink.QdiscAdd(qdisc)
	}
	if err != nil {
		return fmt.Errorf("add qdisc: %w", err)
	}

	// add bpf ingress6 filter to clsact
	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: iface.Attrs().Index,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Handle:    netlink.MakeHandle(0, 1),
			Protocol:  unix.ETH_P_IPV6,
			Priority:  1,
		},
		Fd:           bnatObjs.SchedClsIngress6Nat6.FD(),
		Name:         "nat64",
		DirectAction: true,
	}
	err = netlink.FilterAdd(filter)
	if err != nil {
		return fmt.Errorf("add filter: %w", err)
	}

	// add bpf egress4 filter to clsact
	filter = &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: iface.Attrs().Index,
			Parent:    netlink.HANDLE_MIN_EGRESS,
			Handle:    netlink.MakeHandle(0, 1),
			Protocol:  unix.ETH_P_IP,
			Priority:  1,
		},
		Fd:           bnatObjs.SchedClsEgress4Nat4.FD(),
		Name:         "nat46",
		DirectAction: true,
	}
	err = netlink.FilterAdd(filter)
	if err != nil {
		return fmt.Errorf("add filter: %w", err)
	}

	xlsmSpec, err := loadXlsm()
	if err != nil {
		return fmt.Errorf("load xlsm: %w", err)
	}

	xlsmObjs := &xlsmObjects{}
	err = xlsmSpec.LoadAndAssign(xlsmObjs, nil)
	if err != nil {
		return fmt.Errorf("load and assign: %w", err)
	}
	b.closers = append(b.closers, xlsmObjs)

	lsmBpfLink, err := link.AttachLSM(link.LSMOptions{Program: xlsmObjs.XlsmBpf})
	if err != nil {
		return fmt.Errorf("attach lsm bpf: %w", err)
	}
	b.closers = append(b.closers, lsmBpfLink)

	return nil
}
