/*
 * Use coalition_resource_usage to get a coalition's estimated power usage, in mW.
 * This power usage is
 *
 * Empirically, on M1, this is the same as asking for libpmenergy's "Energy Impact" for each PID in the coalition, despite that supposedly making use of network, disk, and GPU stats.
 * We prefer this because we don't have to loop through the coalition's processes manually, and because taking the samples uses less CPU. Also, we can say this is mW, whereas Energy Impact is officially an opaque / unitless number.
 * Also, this doesn't need root.
 *
 * A coalition is a task group of an app and its child processes (including XPC services such as Virtual Machine Service) as seen in the "Energy" tab of Activity Monitor.
 *
 * Usage: ./powermon2 <pid> [one-shot sampling period in seconds]
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <stdbool.h>

#include <mach/mach.h>
#include <mach/mach_error.h>
#include <mach/mach_time.h>
#include <libproc.h>

// <mach/coalition.h>
#define COALITION_TYPE_RESOURCE  (0)
#define COALITION_TYPE_MAX       (1)
#define COALITION_NUM_TYPES      (COALITION_TYPE_MAX + 1)
#define COALITION_NUM_THREAD_QOS_TYPES   7

struct coalition_resource_usage {
	uint64_t tasks_started;
	uint64_t tasks_exited;
	uint64_t time_nonempty;
	uint64_t cpu_time;
	uint64_t interrupt_wakeups;
	uint64_t platform_idle_wakeups;
	uint64_t bytesread;
	uint64_t byteswritten;
	uint64_t gpu_time;
	uint64_t cpu_time_billed_to_me;
	uint64_t cpu_time_billed_to_others;
	uint64_t energy;
	uint64_t logical_immediate_writes;
	uint64_t logical_deferred_writes;
	uint64_t logical_invalidated_writes;
	uint64_t logical_metadata_writes;
	uint64_t logical_immediate_writes_to_external;
	uint64_t logical_deferred_writes_to_external;
	uint64_t logical_invalidated_writes_to_external;
	uint64_t logical_metadata_writes_to_external;
	uint64_t energy_billed_to_me;
	uint64_t energy_billed_to_others;
	uint64_t cpu_ptime;
	uint64_t cpu_time_eqos_len;     /* Stores the number of thread QoS types */
	uint64_t cpu_time_eqos[COALITION_NUM_THREAD_QOS_TYPES];
	uint64_t cpu_instructions;
	uint64_t cpu_cycles;
	uint64_t fs_metadata_writes;
	uint64_t pm_writes;
	uint64_t cpu_pinstructions;
	uint64_t cpu_pcycles;
};

int coalition_info_resource_usage(uint64_t cid, struct coalition_resource_usage* cru, size_t sz);

// <sys/proc_info.h>
struct proc_pidcoalitioninfo {
	uint64_t coalition_id[COALITION_NUM_TYPES];
	uint64_t reserved1;
	uint64_t reserved2;
	uint64_t reserved3;
};

#define PROC_PIDCOALITIONINFO           20

static coalition_t pid_to_coalition_id(int pid) {
    struct proc_pidcoalitioninfo coalition_info;
    int ret = proc_pidinfo(pid, PROC_PIDCOALITIONINFO, 0, &coalition_info, sizeof(coalition_info));
    if (ret != sizeof(coalition_info)) {
        return 0;
    }

    return coalition_info.coalition_id[COALITION_TYPE_RESOURCE];
}

static uint64_t mach_abs_to_nsec(uint64_t mach_absolute, mach_timebase_info_data_t timebase) {
    return mach_absolute * timebase.numer / timebase.denom;
}

/*
static uint64_t get_coalition_energy(coalition_t cid) {
    struct coalition_resource_usage cru;
    int ret = coalition_info_resource_usage(cid, &cru, sizeof(cru));
    if (ret != 0) {
        return 0;
    }

    // energy_billed_to_me and energy_billed_to_others are for IPC vouchers, usually negligible
    return cru.energy;
}
*/

int main(int argc, char *argv[]) {
    // get task_for_pid
    int pid = atoi(argv[1]);

    coalition_t cid = pid_to_coalition_id(pid);
    if (cid == 0) {
        fprintf(stderr, "pid_to_coalition_id failed\n");
        return 1;
    }

    // arg 2 = sampling period (if present)
    int period_sec = 1;
    bool one_shot = false;
    if (argc >= 3) {
        period_sec = atoi(argv[2]);
        one_shot = true;
    }

    // get timebase
    mach_timebase_info_data_t timebase;
    mach_timebase_info(&timebase);

    // get start values
    uint64_t start_ns = clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
    struct coalition_resource_usage start_cru;
    int ret = coalition_info_resource_usage(cid, &start_cru, sizeof(start_cru));
    if (ret != 0) {
        fprintf(stderr, "coalition_info_resource_usage failed\n");
        return 1;
    }

    struct coalition_resource_usage last_cru = start_cru;
    uint64_t last_time_ns = start_ns;

    while (true) {
        sleep(period_sec);

        uint64_t now_ns = clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
        struct coalition_resource_usage new_cru;
        int ret = coalition_info_resource_usage(cid, &new_cru, sizeof(new_cru));
        if (ret != 0) {
            fprintf(stderr, "coalition_info_resource_usage failed\n");
            return 1;
        }

        double delta_energy_nj = new_cru.energy - last_cru.energy;
        double delta_time_sec = (double)(now_ns - last_time_ns) / 1e9;
        double power_nw = delta_energy_nj / delta_time_sec;
        double power_mw = power_nw / 1e6;
        if (one_shot) {
            printf("%.0f\n", power_mw);
        } else {
            printf("%.1f\n", power_mw);
        }

        last_time_ns = now_ns;
        last_cru = new_cru;

        if (one_shot) {
            break;
        }
    }

    // broken: all numbers other than energy are wrong
    fprintf(stderr, "\n\n");
    double elapsed_sec = (double)(last_time_ns - start_ns) / 1e9;
    fprintf(stderr, "avg power = %.1f mW\n", (double)(last_cru.energy - start_cru.energy) / elapsed_sec / 1e6);
    double percent_cpu = (double)mach_abs_to_nsec(last_cru.cpu_time - start_cru.cpu_time, timebase) / elapsed_sec / 1e9 * 100;
    fprintf(stderr, "avg %%cpu = %.1f%%\n", percent_cpu);
    fprintf(stderr, "  %% P-core = %.1f%%\n", ((double)mach_abs_to_nsec(last_cru.cpu_ptime - start_cru.cpu_ptime, timebase) / elapsed_sec / 1e9 * 100) / percent_cpu * 100);
    // TODO: fix overflow when cpu_ptime > cpu_time
    fprintf(stderr, "  %% E-core = %.1f%%\n", ((double)mach_abs_to_nsec((last_cru.cpu_time - last_cru.cpu_ptime) - (start_cru.cpu_time - start_cru.cpu_ptime), timebase) / elapsed_sec / 1e9 * 100) / percent_cpu * 100);
    fprintf(stderr, "avg wakeups = %.1f wakeups/s\n", (double)((last_cru.platform_idle_wakeups + last_cru.interrupt_wakeups) - (start_cru.platform_idle_wakeups + start_cru.interrupt_wakeups)) / elapsed_sec);

    return 0;
}
