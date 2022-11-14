package embedr

/*
#cgo windows LDFLAGS: -L/usr/local/lib64/R/lib -lm -lR ${SRCDIR}/libembedr.a
#cgo windows CFLAGS: -I${SRCDIR}/../include -IC:/cygwin64/usr/share/R/include -IC:/cygwin64/usr/share/R/gnuwin32/fixed/h
#cgo linux LDFLAGS: -L/usr/local/lib64/R/lib -lm -lR ${SRCDIR}/libembedr.a
#cgo linux CFLAGS: -I${SRCDIR}/../include -I/usr/share/R/include
#include <string.h>
#include <signal.h>
#include "embedr.h"
*/
import "C"

import (
	"fmt"
	//"github.com/shurcooL/go-goon"
)

// as separate functions for using rmqcb as a library
func InitR() {
	C.record_sigaction_to_current_act() // save Go's sigaction

	// R will change some signal handlers, for SIGPIPE; maybe others.
	C.callInitEmbeddedR()

	// begin debug exper
	/*
		var iface interface{}
		//	C.callInitEmbeddedR()
		myRScript := "rnorm(100)" // generate 100 Gaussian(0,1) samples
		var evalErrorOccurred C.int
		res := C.callParseEval(C.CString(myRScript), &evalErrorOccurred)
		if evalErrorOccurred == 0 && res != C.R_NilValue {
			C.Rf_protect(res)
			iface = SexpToIface(res)
			fmt.Printf("\n Embedding R in Golang example: I got back from evaluating myRScript:\n")
			goon.Dump(iface)
			C.Rf_unprotect(1) // unprotect res
		}
		C.callEndEmbeddedR()
	*/
	// end debug exper

	// save R's sigaction, for possible use later;
	// If we do use them, we'll need to make sure
	// that SA_ONSTACK is set on them before restoring any.
	// https://pkg.go.dev/os/signal says of CGO code:
	// "If the non-Go code installs any signal handlers, it must use the SA_ONSTACK
	//  flag with sigaction. Failing to do so is likely to cause the program to crash
	//  if the signal is received. Go programs routinely run with a limited stack,
	//  and therefore set up an alternate signal stack."
	C.record_sigaction_to_my_r_act()

	C.restore_sigaction_from_current_act() // restore Go's sigaction

	// This sequence got us green tests again in dfs/dtc,
	// rather than core dumps on go test -v there. Well,
	// we didn't call C.record_sigaction_to_R_act(), but
	// saving R's sigaction to an array hopefully alters
	// nothing.

}

func EndR() {
	C.callEndEmbeddedR()
}

var RevalErr = fmt.Errorf("RevalErr")

func EvalR(script string) (iface interface{}, err error) {

	var evalErrorOccurred C.int
	res := C.callParseEval(C.CString(script), &evalErrorOccurred)
	if evalErrorOccurred != 0 {
		// if needed, dig in and figure out how to
		// get the error. For now it may be enough
		// to get note there was either a parse or
		// an evaluation error.
		return nil, RevalErr
	}
	if evalErrorOccurred == 0 && res != C.R_NilValue {
		C.Rf_protect(res)
		// do stuff here:
		iface = SexpToIface(res)
		//fmt.Printf("\n Embedding R in Golang example: I got back from evaluating script:\n")
		//goon.Dump(iface)
		C.Rf_unprotect(1) // unprotect res
	}
	return
}
