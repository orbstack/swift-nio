(originally written for commit [`596962b2 hvf: balloon: fix REUSABLE not working on anon/host-untouched memory`](https://github.com/orbstack/libkrun-macvirt/commit/596962b2))

Turns out that the current balloon implementation doesn't work on pages not touched by host since the last host remap.

It also doesn't unaccount pages that have been swapped out, so if the host is under memory pressure, we overaccount and it looks like the balloon didn't do anything. (Probably also applies to compressed pages, as they also need to be faulted in)

For the swapping case:

- vm_pageout_scan -> pmap_disconnect_options -> pmap_page_protect_options
- pmap_page_protect_options removes pages from the pmap
- Someone (mach?) updates ledgers to account for swapping, but leaves phys_footprint unchanged, because the memory still belongs to the vm_map (which is really confusing, because these are task ledgers, but usually updated by pmap)
- The hv_vm_protect(NONE) trick doesn't work for subtracting from phys_footprint when pages are swapped:
  - hv_vm_protect -> vm_map_protect -> pmap_protect_options -> pmap_remove_options
  - pmap_remove_range_options normally subtracts removed pages from phys_footprint
  - However, since the pages were already removed from pmap by swapping, there's nothing to remove
  - They remain accounted to phys_footprint because they're still in swap/compressor
  - For unknown reasons (need to wait for source code), it also doesn't seem to work at all on macOS 15 beta 6

**FIX: Just use hv_vm_unmap + hv_vm_map. hv_vm_protect cannot be made reliable here. This is a bit slower, but unavoidable, and it's more future-proof.**

The case of it not working on pages not touched by the host is a bit more complicated:

- If we call madvise on the host, on pages that are *not* mapped in the host pmap, it gets as far as deactivate_pages_in_object. As expected, it doesn't call into pmap_clear_refmod_range_options to clear the referenced bit, but that's expected because it's not mapped.
- According to vmmap, a successful madvise(REUSABLE) keeps pages resident, but they're no longer considered dirty. So you know our madvise calls aren't working because total virtual size ~= resident ~= dirty.
- You know it *definitely* doesn't work because running memhog to induce pressure on the host, and then checking vmmap after it gets OOM killed, shows that virtual ~= ((resident ~= dirty) + swapped), i.e. swapped + resident ~= virtual. (swapped isn't considered dirty because it's clean when it's first swapped back in)
- In deactivate_pages_in_object: if you test with sysctl vm.madvise_free_debug=1, it appears to be working, because the pages are all zeros after madvise, which is very much not the behavior when vm.madvise_free_debug=0. This means that [all checks for madvise_free_debug pass](https://github.com/apple-oss-distributions/xnu/blob/94d3b452840153a99b38a3a9659680b2a006908e/osfmk/vm/vm_object.c#L2222), so I'm not sure why. (Is it even vmp_dirty that's the problem?)
- It doesn't go to the VM page delayed work path where the pages are actually [added/moved to the inactive queue](https://github.com/apple-oss-distributions/xnu/blob/94d3b452840153a99b38a3a9659680b2a006908e/osfmk/vm/vm_resident.c#L6869).

**FIX: simply touch every page passed to hvf::free_range on the host in order to page it into the pmap, and only then, call madvise(REUSABLE). Since all pages are now in the host pmap, page deactivation now works as expected and subtracts from dirty. Inducing pressure on the host now shows that (resident + swapped) ~= phys_footprint, which is much lower than total virtual size.**

This also applies to madvise(REUSE) -- it doesn't do anything if pages aren't in host pmap. REUSE is even more annoying, because if you prefault and then REUSE, all pages are now accounted to phys_footprint thanks to the host pmap! And double accounted, because they'll also be accounted to the VM pmap on fast fault. So then you have to pmap_remove them, which can, again, only be done reliably by remapping (due to swap messing with the protect(NONE) case). The other problem with NONE is that it could cause SIGSEGV if racing with an access, though we can now handle that using signal handlers on the host.

As a future optimization for REUSE, it might actually be viable to use mprotect(NONE), add a SIGSEGV handler to handle access races, and ignore swap races and rely on remap thread to fix them, as long as it works at all on macOS 15. But it's probably not worth it.

That's the general idea, but we can make a few optimizations here:

- On boot, Linux reports all memory as free, because initial page flags are 0. If we touch every page on the host, we effectively prefault the entire VM's memory -- good for VM performance consistency (like KVM passthrough setups with preallocated hugepages), but really bad for host performance. It's not that bad because we touch and madvise in small batches (max Linux reporting batch size = pageblock size = 32M), but we do have range merging, so now we'd have to limit the merged range size.
- Touching is not actually as slow as it sounds (~365 ms), but it's far from ideal to spike pressure before we relieve it. Prefaulting with MADV_WILLNEED is a bit faster (~295 ms), so do that instead. It's far more likely than not that pages do need to be refaulted because of mach_vm_remap, so this should almost always be worth it. (It's not very fast because it just calls vm_fault repeatedly in the kernel.)
- On the Linux side, page_reporting only applies to the buddy page allocator. The way struct page init works is that all pages are reserved at boot, and then unused memory is freed to the buddy allocator by pageblock. I modified Linux to set the PageReported bit when freeing to buddy at boot, so that we don't prefault the entire VM and spike host memory pressure or burn CPU at boot.
  - This introduces a new inefficiency: the first time Linux uses any page, it'll get reported as REUSEd. That's fine and might even be faster in some cases because page insertion is the more expensive part of faults, and we asynchronously prefault soon-to-be-used memory in this case. It's nowhere near as bad as prefaulting the entire VM.
- Even though the host side takes care of REUSABLE marking, we do still need to unmap+remap the HVF side to clear references before it actually gets marked as pmap. It's not only for phys_footprint accounting cosmetics. Since deactivate_pages_in_object calls pmap_clear_refmod_range_options which modifies all active pmaps, let's unmap, then madvise, then remap, to avoid having to touch multiple pmaps.
  - This might mean that the reason the bug exists is because vmp_dirty=FALSE is set when the last pmap reference with ppattr=reusable is removed? And since there's no host pmap, nothing has ppattr=reusable set?
- Unfortunately, REUSE is still needed, because refaulting pages from an object into a pmap somehow doesn't change phys_footprint -- only inserting new pages does.
  - phys_footprint is added (credited) on page insert, wire, purgable nonvolatile change, ownership change, IOKit map, pmap pagetable alloc, pmap page_protect (compressor case), pmap enter, and pmap CLEAR_REUSABLE
  - phys_footprint is subtracted (debited) on page remove, unwire, free, purgable volatile change, ownership change, IOKit unmap, pmap pagetable free, pmap remove, and pmap SET_REUSABLE
  - pmap_enter would normally fix accounting, but for some reason, it credits task_ledgers.reusable if is_reusable, and credits task_ledgers.phys_footprint if is_internal. is_internal applies to most pages but is [mutually exclusive with is_reusable](https://github.com/apple-oss-distributions/xnu/blob/94d3b452840153a99b38a3a9659680b2a006908e/osfmk/arm/pmap/pmap.c#L6344), so although we do refault all pages used by Linux, they get accounted to the wrong ledger if we don't call REUSE.
  - Now that REUSABLE actually works, we have problems with delayed REUSE again. It's queuing too aggressively and not flushing until the next 10-sec deferrable FPR round, which is too long. We can keep the async queuing, but need to flush it more often based on a threshold on the Linux side to avoid suspiciously low phys_footprint on Linux memory usage spikes. (Currently, we can finish an entire memhog-linux run and get OOM killed before REUSE queues are flushed.)
  - Batched REUSE, and a redundant pmap fast fault later, is still much better than HVF data abort-based page: in that case, we still incur HV vm_fault overhead before getting the abort in userspace (~500ns?), vmexit overhead (~500ns), vCPU loop overhead (125ns), hv_vm_map overhead (+ pmap lock contention with other threads)
  - I really don't see why REUSE needs a pmap fast fault -- I think Apple was just being lazy here. SET_REUSABLE makes sense because it needs to redirty the page if touched, but all CLEAR_REUSABLE does in arm_force_fast_fault_with_flush_range is update the ledgers and ppattr bit. The actual fault it causes later is useless.
  - Why doesn't Apple just fix vmp_reusable or call vm_object_reuse_pages on pmap_enter?? Clearly the pageout thread has no trouble doing it. (VM_PAGEOUT_SCAN_HANDLE_REUSABLE_PAGE)

A fun trick we could do here is to get rid of mach_vm_remap and abuse REUSABLE to clear double accounting, because it doesn't actually undirty pages that are still mapped in the HVF pmap -- and if they're not mapped in there, then they're not being used. This reduces refault overhead (pmap fast faults instead of Mach VM faults) and reduces the chance of needing to refault at all before the next REUSABLE call, but it's relying on dangerous undocumented behavior (that any reference pins the page for undirtying purposes) and is much slower to remap (95ms for full REUSABLE vs. 17ms for mach_vm_remap).

(Also, I don't know why pmap is so slow on M3. According to [Apple docs](https://support.apple.com/guide/security/operating-system-integrity-sec8b776536b/web), SPTM/PPL is only used on iOS, but I don't know whether we're running osfmk/arm64/sptm/pmap/pmap.c or osfmk/arm/pmap/pmap.c. I've been reading the SPTM implementation, which is probably wrong... but we do go through ppl_dispatch. It's cool that Mach VM is flexible enough for HVF to simply dispatch guest aborts to a vm_map, but Mach VM is also spaghetti.)

In practice this bug is *really* bad: effectively all anon memory can't be reclaimed by the balloon, because the host doesn't touch it (unless Linux reused it, and it used to be a file or slab page that was used for virtio-blk, virtiofs, or an skb page used for virtio-net). Although many workloads will mostly use FS, most will still be more anon-heavy, so the balloon basically doesn't work.

During early development, I was mostly testing with the virtiofs fd workload because it stresses slab fragmentation on the Linux side, and causes a lot of double accounting because over time, due to pressure and compaction/frag on the Linux side, almost every page gets touched by the host for virtio/virtiofs/HVC shm usage.

So this went unnoticed because

- I have too much RAM so there wasn't much compression or swapping
  - And having too much RAM means I don't actually notice memory pressure
- Without swapping, phys_footprint accounting is correct on macOS 14 due to our pmap ledger clearing tricks
- I was mostly testing with non-anon (i.e. virtiofs) use cases, which aren't affected by this bug
  - This was before I wrote memhog, so there was no easy way to test anon memory with random data, which is bad because zero pages (tail /dev/zero) are highly compressible
- Testing with vm.madvise_free_debug=1 appears to work
- I couldn't find the problem through static analysis of XNU code (and still can't...)

What a mess.

The test case that uncovered this:

- Run memhog-linux (gcc exp/mem/memhog.c -O2 -o memhog-linux) on Linux
- Wait for it to get OOM killed. This fills guest memory with incompressible data.
- Check `sudo vmmap 'OrbStack Helper'` on macOS. Observe that virtual ~= resident ~= dirty.
- Run memhog on macOS
- Wait for it to get killed
- Check vmmap again. Observe that
  - If working: resident, dirty, swapped are all low
  - If not working: resident + swapped is high (~= virtual)
