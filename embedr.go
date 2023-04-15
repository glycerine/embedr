package embedr

/*
#cgo windows LDFLAGS: -L/usr/local/lib64/R/lib -lm -lR ${SRCDIR}/libembedr.a
#cgo windows CFLAGS: -I${SRCDIR}/../include -IC:/cygwin64/usr/share/R/include -IC:/cygwin64/usr/share/R/gnuwin32/fixed/h
#cgo linux LDFLAGS: -L/usr/local/lib64/R/lib -lm -lR ${SRCDIR}/libembedr.a
#cgo linux CFLAGS: -I${SRCDIR}/../include -I/usr/share/R/include
#cgo darwin LDFLAGS: -L/Library/Frameworks/R.framework/Versions/Current/Resources/lib -lm -lR
#cgo darwin CFLAGS: -I${SRCDIR}/../include -I/Library/Frameworks/R.framework/Versions/Current/Headers
#include <string.h>
#include <signal.h>
#include "embedr.h"
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
	//"github.com/shurcooL/go-goon"
)

// as separate functions for using rmqcb as a library
func InitR(repl bool) {
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

	if repl {
		C.callInitEmbeddedREPL()
	} else {
		// quiet, no prompt.
		C.callInitEmbeddedR()
	}

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

var mutInit sync.Mutex
var initDone bool

// setup to retreive the lastexp from the top level callback, if it succeeded.
// allows Lastexpr() to work.
func registerGetLastTopExpression() int {
	num := C.RegisterMyEmbedrToplevelCallback()
	//fmt.Printf("topTaskCallback registered and got num = %v\n", num)
	return int(num)
}

func ReplDLLinit() {
	// only Register once!
	mutInit.Lock()
	defer mutInit.Unlock()
	if initDone {
		return
	}
	initDone = true

	C.R_ReplDLLinit()
	registerGetLastTopExpression()

	// If ReplDLLinit has installed any
	// signal handlers, make sure they
	// are using the SA_ONSTACK flag,
	// or else the go runtime will
	// panic when it gets a signal.
	C.set_SA_ONSTACK()
}
func ReplDLLdo1() int {

	return int(C.R_ReplDLLdo1())
}

// Return the expression, as a string, from the last evaluation.
// Returns empty string if last expression gave an error.
func Lastexpr() string {
	if C.lastSucessExpression != nil {
		lastexpr := C.GoString(C.lastSucessExpression)
		//vv("Lastexpr() sees = '%v'", lastexpr)
		C.lastSucessExpression = nil
		return lastexpr
	}
	return ""
}

// only gave raw values, not nice print() outputs like we actually want.
// Since can be slow and hog memory to stringify all output, turn this off.
// func Lastvalue() string {
// 	if C.lastValue != nil {
// 		lastval := C.GoString(C.lastValue)
// 		vv("Lastvalue() sees = '%v'", lastval)
// 		C.lastValue = nil
// 		return lastval
// 	}
// 	return ""
// }

// or run_Rmainloop(); but one-step R_ReplDLLdo1() is nice
// in that we get control back
// after each top level something (command?)
// https://cran.r-project.org/doc/manuals/R-exts.html#index-Rf_005finitEmbeddedR
// section 8.1
//
// "Rf_initEmbeddedR sets R to be in interactive mode: you can
//  set R_Interactive (defined in Rinterface.h) subsequently to change this."
//
func SimpleREPL() {

	ReplDLLinit()
	for {
		did := ReplDLLdo1()
		vv("back from one call to R_ReplDLLdo1(); did = %v\n", did)
		// did == 0 => error evaluating
		// did == -1 => ctrl-d (end of file).

		if did <= 0 {
			break
		}
		vv("lastexpr = '%v'", Lastexpr())
	}
}

// The default R prompt is "> ", but
// this can be changed. It won't take
// effect until a top-level action has
// ocurred, however.
// Calls injectCustomPrompt(), which uses callParseEval,
// so the R interpreter must already be
// initialized.
func SetCustomPrompt(prompt string) {
	//vv("SetCustomPrompt called with prompt = '%v'", prompt)

	// free of 0 is a no-op.
	// Release any previously allocated prompt.
	C.free(unsafe.Pointer(C.customPrompt))

	// malloc and set the new prompt.
	C.customPrompt = C.CString(prompt)

	// uses callParseEval, so the R interpreter must have be initialized.
	C.injectCustomPrompt()
}

var cleanupFunc func()

func SetGoCallbackForCleanup(f func()) {
	cleanupFunc = f

	//C.restore_all_starting_signal_handlers_WITH_SA_ONSTACK()
}

//export GoCleanupFunc
func GoCleanupFunc() {

	// This is called at R shutdown time (e.g. user calling q() ) by doing:
	//
	// embedr.ReplDLLinit()
	// embedr.SetGoCallbackForCleanup(func() { /* golang cleanup functions called here. */ })
	// embedr.EvalR(`.Last.sys=function(){.C("CallGoCleanupFunc")}`)
	//  then the C code in cpp/embedr.cpp CallGoCleanupFunc() calls us;
	//  (which is why we have to be exported) and
	//  then we can call our cleanupFunc.

	//fmt.Printf("GoCleanupFunc called.\n")
	if cleanupFunc != nil {
		cleanupFunc()
	}
	//fmt.Printf("GoCleanupFunc done.\n")
}

func Record_sigaction_to_current_act() {
	C.record_sigaction_to_current_act()
}

func Restore_sigaction_from_current_act() {
	C.restore_sigaction_from_current_act()
}

func Restore_all_starting_signal_handlers_WITH_SA_ONSTACK() {
	C.restore_all_starting_signal_handlers_WITH_SA_ONSTACK()
}

// set the SA_ONSTACK flag on all signal
// handlers currently in place.
func Set_SA_ONSTACK() {
	C.set_SA_ONSTACK()
}

// try having a callback for svv() to save graphs in a loop.

var rCallbackToGoFunc func()

func SetRCallbackToGoFunc(f func()) {
	rCallbackToGoFunc = f
}

//export RCallbackToGoFunc
func RCallbackToGoFunc() {

	//fmt.Printf("rCallbackToGoFunc called.\n")
	if rCallbackToGoFunc != nil {
		rCallbackToGoFunc()
	}
	//fmt.Printf("rCallbackToGoFunc done.\n")
}
