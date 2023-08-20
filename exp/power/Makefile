all: powermon powermon2

powermon2: powermon2.c
	gcc powermon2.c -Wall -o powermon2 -O2

powermon: powermon.c
	gcc powermon.c -Wall -o powermon -O2 -lpmenergy -lpmsample
