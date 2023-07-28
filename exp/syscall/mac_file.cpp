#include <thread>
#include <cstdio>
#include <ctime>
#include <unistd.h>
#include <sys/socket.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/xattr.h>

typedef unsigned long long u64;
typedef long long s64;

#define NS 1
#define US (1000 * NS)
#define MS (1000 * US)
#define SEC (1000 * MS)

#define DURATION (10ULL * SEC)
#define BUCKET_SIZE 5

static u64 now() {
	return clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
}

static int do_test() {
	u64 buckets[65536] = {0};
	u64 total_lat = 0;
	u64 iters = 0;

	int dir_fd = open("/Users/dragon/code/projects/macvirt/exp/syscall", O_RDONLY|O_DIRECTORY, 0);
	if (dir_fd < 0) {
		printf("open error\n");
		return -1;
	}

	u64 start = now();
	while (true) {
		int new_fd = open("/Users/dragon/code/projects/macvirt/exp/syscall/testfile", O_RDWR|O_CREAT, 0644);
		if (new_fd < 0) {
			printf("open error\n");
			break;
		}

		u64 iter_start_ts = now();
		if (iter_start_ts - start > DURATION) {
			break;
		}

		/* iter begin */
		//close(new_fd);

		// struct stat st;
		// int ret = fstat(new_fd, &st);
		// if (ret != 0) {
		// 	printf("fstat error\n");
		// 	break;
		// }

		// struct stat st;
		// int ret = fstatat(new_fd, "testfile", &st, AT_SYMLINK_NOFOLLOW);
		// if (ret != 0) {
		// 	printf("fstatat error\n");
		// 	break;
		// }

		// char buf[1024];
		// int ret = getxattr("/Users/dragon/code/projects/macvirt/exp/syscall/testfile", "dev.orbstack.perm", buf, sizeof(buf), 0, XATTR_NOFOLLOW);
		// if (ret < 0) {
		// 	printf("getxattr error\n");
		// 	break;
		// }

		// char buf[1024];
		// int ret = fgetxattr(new_fd, "dev.orbstack.perm", buf, sizeof(buf), 0, 0);
		// if (ret < 0) {
		// 	printf("getxattr error\n");
		// 	break;
		// }

		// struct stat st;
		// int ret = fstatat(AT_FDCWD, "/.vol/16777232/64485759", &st, AT_SYMLINK_NOFOLLOW);
		// if (ret != 0) {
		// 	perror("fstatat");
		// 	break;
		// }
		// int new_fd = open("/.vol/16777232/64485759", O_RDWR, 0644);
		// if (new_fd < 0) {
		// 	printf("open error\n");
		// 	break;
		// }

		// char buf[1024];
		// int ret = write(new_fd, buf, sizeof(buf));
		// if (ret < 0) {
		// 	printf("write error\n");
		// 	break;
		// }

		char buf[1024];
		int ret = read(new_fd, buf, sizeof(buf));
		if (ret < 0) {
			printf("read error\n");
			break;
		}

		/* iter end*/

		u64 iter_end_ts = now();
		u64 latency =  (iter_end_ts - iter_start_ts) / 1000;
		//printf("latency: %llu\n", latency);
		total_lat += latency;
		iters++;
		u64 bucket = latency / BUCKET_SIZE;
		if (bucket < 65536) {
			buckets[bucket]++;
		} else {
			buckets[65535]++;
		}

		close(new_fd);
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
	return 0;
}

int main(int argc, char *argv[]) {
	return do_test();
}
