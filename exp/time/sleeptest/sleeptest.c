#include <stdio.h>
#include <time.h>

typedef unsigned long long u64;
typedef long long s64;

#define NS 1
#define US (1000 * NS)
#define MS (1000 * US)
#define SEC (1000 * MS)

u64 testWindow = 3ULL * SEC;

u64 test_times[] = {
	1ULL * NS,
	250ULL * NS,
	1ULL * US,
	250ULL * US,
	500ULL * US,
	1ULL * MS,
	2ULL * MS,
	5ULL * MS,
	10ULL * MS,
	25ULL * MS,
	37ULL * MS,
	250ULL * MS,
	1ULL * SEC
};

u64 now() {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec * SEC + ts.tv_nsec;
}

s64 test_one(u64 sleep_duration, u64 time_limit) {
	struct timespec sleep_ts;
	sleep_ts.tv_sec = sleep_duration / SEC;
	sleep_ts.tv_nsec = sleep_duration % SEC;

	u64 start = now();
	s64 sleep_count = 0;
	while (1) {
		nanosleep(&sleep_ts, NULL);
		sleep_count++;
		if (now() - start > time_limit) {
			break;
		}
	}
	u64 end = now();

	// calculate average delta
	s64 expectedTotalSleep = sleep_duration * sleep_count;
	s64 actualTotalSleep = end - start;
	s64 delta = actualTotalSleep - expectedTotalSleep;
	s64 averageDelta = delta / sleep_count;
	// printf("slept %lld times, expected %lld ns, actual %lld ns, delta %lld ns, average delta %lld ns\n",
	// 	sleep_count, expectedTotalSleep, actualTotalSleep, delta, averageDelta);

	return averageDelta;
}

void print_formatted_duration(s64 ns) {
	if (ns < US) {
		printf("%lld ns", ns);
	} else if (ns < MS) {
		printf("%lld us", ns / US);
	} else if (ns < SEC) {
		printf("%lld ms", ns / MS);
	} else {
		printf("%lld s", ns / SEC);
	}
}

int main() {
	// for _, sleepDuration := range testTimes {
	for (int i = 0; i < sizeof(test_times) / sizeof(test_times[0]); i++) {
		u64 sleepDuration = test_times[i];
		s64 averageDelta = test_one(sleepDuration, testWindow);
		printf("sleep duration: ");
		print_formatted_duration(sleepDuration);
		printf(", average delta: ");
		print_formatted_duration(averageDelta);
		printf("\n");
	}

	return 0;
}
