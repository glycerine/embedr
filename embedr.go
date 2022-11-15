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

	// WOW. Discovered by not acted upon yet: there is a way
	// to get R to not set signal handlers!
	//
	// from Section 8.1.5 Threading Issues of https://cran.r-project.org/doc/manuals/R-exts.html
	//
	// "You may also want to consider how signals are handled: R sets
	// signal handlers for several signals, including SIGINT, SIGSEGV, SIGPIPE,
	// SIGUSR1 and SIGUSR2, but these can all be suppressed by setting the
	// variable R_SignalHandlers (declared in Rinterface.h) to 0."

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

// Getting the full value back can be large and thus slow
// and use alot of memory in iface. Prefer EvarR below
// unless it is really needed.
func EvalR_fullback(script string) (iface interface{}, err error) {

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

// EvalR never returns the value of the script.
// Therefore it avoids getting very large amounts
// of data back after assigning large data
// to a variable in R.
func EvalR(script string) (err error) {

	var evalErrorOccurred C.int
	res := C.callParseEval(C.CString(script), &evalErrorOccurred)
	_ = res
	if evalErrorOccurred != 0 {
		// if needed, dig in and figure out how to
		// get the error. For now it may be enough
		// to get note there was either a parse or
		// an evaluation error.
		return RevalErr
	}
	return nil
}

func DemoTaskCallback() {
	num := C.RegisterMyEmbedrToplevelCallback()
	fmt.Printf("DemoTaskCallback registered and got num = %v\n", num)
}

// or run_Rmainloop(); but this is nice in that we get control back
// after each top level something (command?)
// https://cran.r-project.org/doc/manuals/R-exts.html#index-Rf_005finitEmbeddedR
// section 8.1
//
// "Rf_initEmbeddedR sets R to be in interactive mode: you can
//  set R_Interactive (defined in Rinterface.h) subsequently to change this."
//
func SimpleREPL() {
	C.R_ReplDLLinit()
	for {
		did := C.R_ReplDLLdo1()
		vv("back from one call to R_ReplDLLdo1(); did = %v\n", did)
		if did <= 0 {
			break
		}
		// did == 0 => error evaluating
		// did == -1 => ctrl-d (end of file).
	}
}
