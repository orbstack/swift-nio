/*
 * Use libpmenergy to get a PID's "Energy Impact" as seen by Activity Monitor.
 * Empirically, on M1, this is the same as looping over each PID in a coalition and using coalition_resource_usage instead.
 * This samples network stats, disk stats, GPU, etc. but it doesn't actually seem to affect anything. The value it ends up returning is the same.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <stdbool.h>

#include <mach/mach.h>
#include <mach/mach_error.h>
#include <mach/mach_time.h>

struct opaque_pm_task_energy_data {
    uint8_t data[408];
};

int pm_sample_task(mach_port_t task, struct opaque_pm_task_energy_data* pm_energy, uint64_t mach_time, uint32_t flags);
// From libpmenergy.dylib
double pm_energy_impact(struct opaque_pm_task_energy_data* pm_energy);

static double get_energy_impact(mach_port_t task, uint64_t mach_time) {
    struct opaque_pm_task_energy_data energy_info;
    // to disable network stats sampling: flags & ~0x8
    if (pm_sample_task(task, &energy_info, mach_time, 0xffffffff) != 0) {
        return 0.0;
    }
    return pm_energy_impact(&energy_info);
}

static uint64_t mach_absolute_to_nsec(uint64_t mach_absolute, mach_timebase_info_data_t timebase) {
    return mach_absolute * timebase.numer / timebase.denom;
}

static double get_energy_impacts(mach_port_t* tasks, int task_count, uint64_t mach_time) {
    double energy_impact = 0.0;
    for (int taski = 0; taski < task_count; taski++) {
        energy_impact += get_energy_impact(tasks[taski], mach_time);
    }
    return energy_impact;
}

int main(int argc, char *argv[]) {
    // get task_for_pid
    mach_port_t tasks[1024];
    int task_count = argc - 1;
    for (int argi = 1; argi < argc; argi++) {
        int pid = atoi(argv[argi]);
        kern_return_t kr = task_for_pid(mach_task_self(), pid, &tasks[argi - 1]);
        if (kr != KERN_SUCCESS) {
            printf("task_for_pid failed: %s\n", mach_error_string(kr));
            return 1;
        }
    }

    // get timebase
    mach_timebase_info_data_t timebase;
    mach_timebase_info(&timebase);

    // get energy impact
    uint64_t now_abs = mach_absolute_time();
    uint64_t last_time_abs = now_abs;
    double last_energy_impact = get_energy_impacts(tasks, task_count, now_abs);

    while (true) {
        sleep(1);

        now_abs = mach_absolute_time();
        double new_energy_impact = get_energy_impacts(tasks, task_count, now_abs);
        double delta_energy = new_energy_impact - last_energy_impact;
        double delta_time = mach_absolute_to_nsec(now_abs - last_time_abs, timebase) / 1e9;
        double d_energy_impact = delta_energy / delta_time;
        printf("%.3f\n", d_energy_impact);

        last_time_abs = now_abs;
        last_energy_impact = new_energy_impact;
    }

    return 0;
}
