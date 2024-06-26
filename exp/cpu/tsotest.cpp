#include <sys/prctl.h>
#include <thread>
#include <stdio.h>
#include <sys/time.h>
#include <cstdlib>

volatile unsigned variable1 = 0;
#define ITERATIONS 50000000
//#define ITERATIONS 5000000000
void *writer(volatile unsigned *variable2) {
        for (;;) {
                variable1 = variable1 + 1;
                *variable2 = *variable2 + 1;
        }
        return NULL;
}

void *reader(volatile unsigned *variable2) {
        struct timeval start, end;
        gettimeofday(&start, NULL);
        unsigned i;
        unsigned failureCount = 0;
        for (i=0; i < ITERATIONS; i++) {
                unsigned v2 = *variable2;
                unsigned v1 = variable1;
                if (v2 > v1) failureCount++;
        }
        gettimeofday(&end, NULL);
        double seconds = end.tv_sec + end.tv_usec / 1000000. - start.tv_sec - start.tv_usec / 1000000.;
        printf("%u failure%s (%2.1f percent of the time) in %2.1f seconds\n",
               failureCount, failureCount == 1 ? "" : "s",
               (100. * failureCount) / ITERATIONS, seconds);
        exit(0);
        return NULL;
}

int main(int argc, char **argv) {
        prctl(0x4d4d444c, atoi(argv[1]), 0, 0, 0);
        volatile unsigned variable2 = 0;
        std::thread t1(writer, &variable2);
        std::thread t2(reader, &variable2);
        t1.join();
        t2.join();
    return 0;
}
