#include <thread>
#include <cstdio>
#include <ctime>
#include <unistd.h>
#include <sys/socket.h>

typedef unsigned long long u64;
typedef long long s64;

#define NS 1
#define US (1000 * NS)
#define MS (1000 * US)
#define SEC (1000 * MS)

#define DURATION (10ULL * SEC)
#define BUCKET_SIZE 3

static u64 now() {
	return clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
}

static int do_test(int fd1, int fd2) {
	// test ipc writing latency
	std::thread reader([fd1]() {
		u64 buckets[65536] = {0};
		u64 total_lat = 0;
		u64 iters = 0;

		while (true) {
			u64 recv_ts;
			int n = read(fd1, &recv_ts, sizeof(recv_ts));
			if (n != sizeof(recv_ts)) {
				printf("read error\n");
				break;
			}
			u64 send_ts = now();
			u64 latency =  (send_ts - recv_ts) / 1000;
			//printf("latency: %llu\n", latency);
			total_lat += latency;
			iters++;
			u64 bucket = latency / BUCKET_SIZE;
			if (bucket < 65536) {
				buckets[bucket]++;
			} else {
				buckets[65535]++;
			}
		}

		// print avg
		printf("avg latency: %llu\n", total_lat / iters);

		// print median
		u64 sum = 0;
		for (int i = 0; i < 65536; i++) {
			sum += buckets[i];
			if (sum > iters / 2) {
				printf("median: %d\n", i*BUCKET_SIZE);
				break;
			}
		}

		printf("\n");

		// print buckets
		for (int i = 0; i < 65536; i++) {
			if (buckets[i] > 1) {
				printf("%d-%d: %llu\n", i*BUCKET_SIZE, (i+1)*BUCKET_SIZE, buckets[i]);
			}
		}
	});

	u64 start = now();
	while (true) {
		u64 send_ts = now();
		if (send_ts - start > DURATION) {
			break;
		}
		int n = write(fd2, &send_ts, sizeof(send_ts));
		if (n != sizeof(send_ts)) {
			printf("write error\n");
			break;
		}
		usleep(1000);
	}

	close(fd1);
	close(fd2);
	reader.join();
	return 0;
}

static int run_with_pipe() {
	int fds[2];
	int ret = pipe(fds);
	if (ret < 0) {
		printf("pipe error\n");
		return -1;
	}
	return do_test(fds[0], fds[1]);
}

static int run_with_socket_stream() {
	int fds[2];
	int ret = socketpair(AF_UNIX, SOCK_STREAM, 0, fds);
	if (ret < 0) {
		printf("socketpair error\n");
		return -1;
	}
	return do_test(fds[0], fds[1]);
}

static int run_with_socket_dgram() {
	int fds[2];
	int ret = socketpair(AF_UNIX, SOCK_DGRAM, 0, fds);
	if (ret < 0) {
		printf("socketpair error\n");
		return -1;
	}
	return do_test(fds[0], fds[1]);
}

int main(int argc, char *argv[]) {
	return run_with_pipe();
}
