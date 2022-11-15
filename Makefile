
# reduction from ../../src/Makefile

all:
	gcc -v -fPIC -O2 -c -o embedr.o cpp/embedr.cpp -Iinclude/ -I/usr/share/R/include -IC:/cygwin64/usr/share/R/include -IC:/cygwin64/usr/share/R/gnuwin32/fixed/h
	ar cr libembedr.a embedr.o
	go install
