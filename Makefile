
# reduction from ../../src/Makefile

UNAME := $(shell uname)

EXTRAFLAGS := 

ifeq ($(UNAME), Linux)

endif
ifeq ($(UNAME), Darwin)
  EXTRAFLAGS = -I/Library/Frameworks/R.framework/Headers -I/Library/Frameworks/R.framework/Versions/Current/Headers
endif

all:
	go clean # this seemed to fix the use of stale cgo output
	rm -f embedr.o libembedr.a
	gcc -v -fPIC -O2 -c -o embedr.o cpp/embedr.cpp -Iinclude/ -I/usr/share/R/include -IC:/cygwin64/usr/share/R/include -IC:/cygwin64/usr/share/R/gnuwin32/fixed/h $(EXTRAFLAGS)
	ar cr libembedr.a embedr.o
	go install
