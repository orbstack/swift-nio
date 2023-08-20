all: powermon powermon2 powermon_all

powermon2: powermon2.c
	gcc powermon2.c -Wall -o powermon2 -O2

powermon: powermon.c
	gcc powermon.c -Wall -o powermon -O2 -lpmenergy -lpmsample

powermon_all: powermon_all.c
	gcc powermon_all.c -Wall -o powermon_all -O2
