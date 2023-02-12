fio --filename=fio.tmp --direct=1 --rw=randrw --bs=32k --ioengine=mmap --iodepth=256 --runtime=10 --numjobs=4 --time_based --group_reporting --name=iops-test-job --eta-newline=1 --size=1G
