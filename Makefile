
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
	##if [ -f /usr/bin/libtool ]; then libtool -static -o libembedr.a embedr.o; else ar cr libembedr.a embedr.o; fi
	ar cr libembedr.a embedr.o
	## if [ -f /usr/bin/libtool ]; then /Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin/ar -qc libembedr.a embedr.o; /Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin/ranlib libembedr.a; else ar cr libembedr.a embedr.o; fi
	## trying full paths to Xcode ar because of
	## proseek's suggestion https://stackoverflow.com/questions/30948807/static-library-link-issue-with-mac-os-x-symbols-not-found-for-architecture-x8
	go install
