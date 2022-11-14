package embedr

// derived from ../../src/rmqcb/rmqcb.go

//
// Copyright 2015 Jason E. Aten
// License: Apache 2.0. http://www.apache.org/licenses/LICENSE-2.0
//

/*
#cgo LDFLAGS: -L/usr/local/lib64/R/lib -lm -lR ${SRCDIR}/libembedr.a
#cgo CFLAGS: -I${SRCDIR}/../include -I/usr/share/R/include
#include <string.h>
#include <signal.h>
#include "embedr.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
	"github.com/shurcooL/go-goon"
	"github.com/ugorji/go/codec"
)

// restore R's signal handler for SIGINT (and all others)
func init() {
	// The Go runtime is the host. This code will be run
	// Go -> R -> here, when R loads the Go code. Uh oh. Now
	//  we have two Go runtimes?
	//C.restore_all_starting_signal_handlers()
	//C.set_SA_ONSTACK()
}

func getAddr(addr_ C.SEXP) (*net.TCPAddr, error) {

	if C.TYPEOF(addr_) != C.STRSXP {
		return nil, fmt.Errorf("getAddr() error: addr is not a string STRXSP; instead it is type %d. addr argument must be a string of form 'ip:port'\n", C.TYPEOF(addr_))
	}

	caddr := C.R_CHAR(C.STRING_ELT(addr_, 0))
	addr := C.GoString(caddr)

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil || tcpAddr == nil {
		return nil, fmt.Errorf("getAddr() error: address '%s' could not be parsed by net.ResolveTCPAddr(): error: '%s'", addr, err)
	}
	return tcpAddr, nil
}

func getTimeoutMsec(timeout_msec_ C.SEXP) (int, error) {
	if C.TYPEOF(timeout_msec_) != C.REALSXP || int(C.Rf_xlength(timeout_msec_)) != 1 {
		return 0, fmt.Errorf("getTimeoutMsec() error: timeout_msec must be a single number; it should convey the number of milliseconds to wait before timing out the call.")
	}

	return int(C.get_real_elt(timeout_msec_, 0)), nil
}

var upgrader = websocket.Upgrader{} // use default options

//export ListenAndServe
//
// ListenAndServe is the server part that expects calls from client
// in the form of RmqWebsocketCall() invocations.
// The underlying websocket library is the battle tested
// https://github.com/gorilla/websocket library from the
// Gorilla Web toolkit. http://www.gorillatoolkit.org/
//
// addr_ is a string in "ip:port" format. The server
// will bind this address and port on the local host.
//
// handler_ is an R function that takes a single argument.
// It will be called back each time the server receives
// an incoming message. The returned value of handler
// becomes the reply to the client.
//
// rho_ is an R environment in which the handler_ callback
// will occur. The user-level wrapper rmq.server() provides
// a new environment for every call back by default, so
// most users won't need to worry about rho_.
//
// Return value: this is always R_NilValue.
//
// Semantics: ListenAndServe() will start a new
// webserver everytime it is called. If it exits
// due to a call into R_CheckUserInterrupt()
// or Rf_error(), then a background watchdog goroutine
// will notice the lack of heartbeating after 300ms,
// and will immediately shutdown the listening
// websocket server goroutine. Hence cleanup
// is fairly automatic.
//
// Signal handling:
//
// SIGINT (ctrl-c) is noted by R, and since we
// regularly call R_CheckUserInterrupt(), the
// user can stop the server by pressing ctrl-c
// at the R-console. The go-runtime, as embedded
// in the c-shared library, is not accustomed to being
// embedded yet, and so its (system) signal handling
// facilities (e.g. signal.Notify) should *not* be
// used. We go to great pains to actually preserve
// the signal handling that R sets up and expects,
// as allowing the go runtime to see any signals just
// creates heartache and crashes.
//
func ListenAndServe(addr_ C.SEXP, handler_ C.SEXP, rho_ C.SEXP) C.SEXP {

	addr, err := getAddr(addr_)

	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(err.Error()))
		return C.R_NilValue
	}

	if 0 == int(C.isFunction(handler_)) { // 0 is false
		C.ReportErrorToR_NoReturn(C.CString("‘handler’ must be a function"))
		return C.R_NilValue
	}

	if rho_ != nil && rho_ != C.R_NilValue {
		if 0 == int(C.isEnvironment(rho_)) { // 0 is false
			C.ReportErrorToR_NoReturn(C.CString("‘rho’ should be an environment"))
			return C.R_NilValue
		}
	}

	fmt.Printf("ListenAndServe listening on address '%s'...\n", addr)

	// Motivation: One problem when acting as a web server is that
	// webSockHandler will be run on a separate goroutine and this will surely
	// be a separate thread--distinct from the R callback thread. This is a
	// problem because if we call back into R from the goroutine thread
	// instead of R's thread, R will see the small stack and freak out.
	//
	// So: we'll use a channel to send the request to the main R thread
	// for call back into R. The *[]byte passed on these channels represent
	// msgpack serialized R objects.
	requestToRCh := make(chan *[]byte)
	replyFromRCh := make(chan *[]byte)
	reqStopCh := make(chan bool)
	doneCh := make(chan bool)
	var lastControlHeartbeatTimeNano int64

	beatHeart := func() {
		now := int64(time.Now().UnixNano())
		atomic.StoreInt64(&lastControlHeartbeatTimeNano, now)
	}

	webSockHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, "Not found", 404)
			return
		}

		if r.Method != "GET" {
			http.Error(w, "Method not allowed, only GET allowed.", 405)
			return
		}

		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			msg := fmt.Sprintf("server webSockHandler() handler saw "+
				"websocket upgrader.Upgrade() error: '%s'", err)
			fmt.Printf("%s\n", msg)
			http.Error(w, msg, 500)
			return
		}
		defer c.Close()

		_, message, err := c.ReadMessage()
		if err != nil {
			msg := fmt.Sprintf("server webSockHandler() handler saw "+
				"websocket ReadMessage() error: '%s'", err)
			fmt.Printf("%s\n", msg)
			http.Error(w, msg, 500)
			return
		}

		requestToRCh <- &message
		reply := <-replyFromRCh

		err = c.WriteMessage(websocket.BinaryMessage, *reply)
		if err != nil {
			msg := fmt.Sprintf("server webSockHandler() handler saw "+
				"websocket WriteMessage() error: '%s'", err)
			fmt.Printf("%s\n", msg)
			http.Error(w, msg, 500)
			return
		}
	} // end webSockHandler

	// start a new server, to avoid registration issues with
	// the default http library mux/server which may be in use
	// already for other purposes.
	mux := http.NewServeMux()
	mux.HandleFunc("/", webSockHandler)
	server := NewWebServer(addr.String(), mux)
	server.Start()

	// This watchdog will shut the webserver down
	// if lastControlHeartbeatTimeNano goes
	// too long without an update. This will
	// happen when C.R_CheckUserInterrupt()
	// doesn't return below in the main control loop.
	beatHeart()
	go func() {
		for {
			select {
			case <-time.After(time.Millisecond * 100):
				last := atomic.LoadInt64(&lastControlHeartbeatTimeNano)
				lastTm := time.Unix(0, last)
				deadline := lastTm.Add(300 * time.Millisecond)
				now := time.Now()
				if now.After(deadline) {
					VPrintf("\n web-server watchdog: no heartbeat "+
						"after %v, shutting down.\n", now.Sub(lastTm))
					server.Stop()
					return
				}
			}
		}
	}()

	// This is the main control routine that lives on the main R thread.
	// All callbacks into R must come from this thread, as it is a
	// C thread with a big stack. Go routines have tiny stacks and
	// R detects this and crashes if you try to call back from one of them.
	for {

		select {
		case <-time.After(time.Millisecond * 100):
			//
			// Our heartbeat logic:
			//
			// R_CheckUserInterrupt() will check if Ctrl-C
			// was pressed by user and R would like us to stop.
			// (R's SIGINT signal handler is installed in our
			// package init() routine at the top of this file.)
			//
			// Note that R_CheckUserInterrupt() may not return!
			// So, Q: how will the server know to cancel/cleanup?
			// A: we'll have the other goroutines check the following
			// timestamp. If it is too out of date, then they'll
			// know that they should cleanup.
			beatHeart()
			C.R_CheckUserInterrupt()

		case msgpackRequest := <-requestToRCh:

			rRequest := decodeMsgpackToR(*msgpackRequest)
			C.Rf_protect(rRequest)

			// Call into the R handler_ function, and get its reply.
			R_fcall := C.lang2(handler_, rRequest)
			C.Rf_protect(R_fcall)
			if Verbose {
				C.PrintToR(C.CString("listenAndServe: got msg, just prior to eval.\n"))
			}
			evalres := C.eval(R_fcall, rho_)
			C.Rf_protect(evalres)
			if Verbose {
				C.PrintToR(C.CString("listenAndServe: after eval.\n"))
			}

			// send back the reply, first converting to msgpack
			reply := encodeRIntoMsgpack(evalres)
			C.Rf_unprotect(3)
			replyFromRCh <- &reply

		case <-reqStopCh:
			// not sure who should close(reqStopCh). At the moment it isn't used.
			close(doneCh)
			return C.R_NilValue
		}
	}
}

//export RmqWebsocketCall
//
// RmqWebsocketCall() is the client part that talks to
// the server part waiting in ListenAndServe().
// ListenAndServe is the server part that expects calls from client
// in the form of RmqWebsocketCall() invocations.
// The underlying websocket library is the battle tested
// https://github.com/gorilla/websocket library from the
// Gorilla Web toolkit. http://www.gorillatoolkit.org/
//
// addr_ is an "ip:port" string: where to find the server;
// it should match the addr_ the server was started with.
//
// msg_ is the R object to be sent to the server.
//
// timeout_msec_ is a numeric count of milliseconds to
// wait for a reply from the server. Timeouts are the
// only way we handle servers that accept our connect
// and then crash or take too long. Although a timeout
// of 0 will wait forever, this is not recommended.
// SIGINT (ctrl-c) will not interrupt a waiting client,
// so do be sure to give it some sane timeout. The
// default is 5000 msec (5 seconds).
//
func RmqWebsocketCall(addr_ C.SEXP, msg_ C.SEXP, timeout_msec_ C.SEXP) C.SEXP {

	addr, err := getAddr(addr_)

	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(err.Error()))
		return C.R_NilValue
	}

	timeout, err := getTimeoutMsec(timeout_msec_)

	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(err.Error()))
		return C.R_NilValue
	}

	var deadline time.Time
	if timeout != 0 {
		deadline = time.Now().Add(time.Duration(timeout) * time.Millisecond)
	}

	// marshall msg_ into msgpack []byte
	msgpackRequest := encodeRIntoMsgpack(msg_)

	VPrintf("rmq says: before client_main().\n")
	reply, err := client_main(addr, msgpackRequest, deadline)
	VPrintf("rmq says: after client_main(), err = '%#v'.\n", err)

	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(err.Error()))
		return C.R_NilValue
	}

	if reply == nil || len(reply) <= 0 {
		return C.R_NilValue
	}
	return decodeMsgpackToR(reply)
}

//export Callcount
func Callcount() C.SEXP {
	fmt.Printf("count = %d\n", count)
	return C.R_NilValue
}

func main() {
	// We always need a main() function to make possible
	// CGO compiler to compile the package as C shared library
	// The 'import "C"' at the top of the file is also required
	// in order to export functions marked with //export

	// Note that main() is not run when the .so library is loaded.

	// However, since this is main, it is also the natural place
	// to give an example also of how to
	// embed R in a Go program, should you wish to do that as
	// well.

	// Note that the following go code is an optional demonstration,
	// and not a part of the R package rmq functionality at its core.

	// Introduction to embedding R:
	//
	// While RMQ is mainly designed to embed Go under R, it
	// defines functions that make embedding R in Go
	// quite easy too. We use SexpToIface() to generate
	// a go inteface{} value. For simple uses, this may be
	// more than enough.
	//
	// If you wish to turn results into
	// a pre-defined Go structure, the interface{} value could
	// transformed into msgpack (as in encodeRIntoMsgpack())
	// and from there automatically parsed into Go structures
	// if you define the Go structures and use
	// https://github.com/glycerine/greenpack (descended
	// from tinylib/msgpack) to generate the
	// go struct <-> msgpack encoding/decoding boilerplate.
	// The greenpack library uses go generate and is
	// blazing fast. This also avoids maintaining a separate
	// IDL file. Your Go source code is always the defining document.

	var iface interface{}
	C.callInitEmbeddedR()
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
}

var count int

// A zero value for deadline means writes will not time out.
func client_main(addr *net.TCPAddr, msg []byte, deadline time.Time) ([]byte, error) {

	var err error
	u := url.URL{Scheme: "ws", Host: addr.String(), Path: "/"}
	VPrintf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		err := fmt.Errorf("rmq::client_main(), websocket.DefaultDialer() attempt to connect to '%s' resulted in error: '%s'", u.String(), err)
		return nil, err
	}
	defer func() {
		if c != nil {
			c.Close()
		}
	}()

	err = c.SetWriteDeadline(deadline)
	if err != nil {
		return nil, fmt.Errorf("rmq::client_main(), c.SetWriteDeadline(deadline) to '%s' resulted in error: '%s'", u.String(), err)
	}

	err = c.WriteMessage(websocket.BinaryMessage, msg)
	if err != nil {
		return nil, fmt.Errorf("rmq::client_main(), c.WriteMessage() to '%s' resulted in error: '%s'", u.String(), err)
	}

	err = c.SetReadDeadline(deadline)
	if err != nil {
		return nil, fmt.Errorf("rmq::client_main(), c.SetReadDeadline(deadline) to '%s' resulted in error: '%s'", u.String(), err)
	}

	_, replyBytes, err := c.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("rmq::client_main(), c.ReadMessage() to '%s' resulted in error: '%s'", u.String(), err)
	}
	count++

	return replyBytes, nil
}

func panicOn(err error) {
	if err != nil {
		panic(err)
	}
}

type MsgpackHelper struct {
	initialized bool
	mh          codec.MsgpackHandle
	jh          codec.JsonHandle
}

func (m *MsgpackHelper) init() {
	if m.initialized {
		return
	}

	m.mh.MapType = reflect.TypeOf(map[string]interface{}(nil))

	// configure extensions
	// e.g. for msgpack, define functions and enable Time support for tag 1
	//does this make a differenece? m.mh.AddExt(reflect.TypeOf(time.Time{}), 1, timeEncExt, timeDecExt)
	m.mh.RawToString = true
	m.mh.WriteExt = true
	m.mh.SignedInteger = true
	m.mh.Canonical = true // sort maps before writing them

	// JSON
	m.jh.MapType = reflect.TypeOf(map[string]interface{}(nil))
	m.jh.SignedInteger = true
	m.jh.Canonical = true // sort maps before writing them

	m.initialized = true
}

var h MsgpackHelper

func init() {
	h.init()
}

// returns an unprotected SEXP
func decodeMsgpackToR(reply []byte) C.SEXP {

	h.init()
	var r interface{}

	decoder := codec.NewDecoderBytes(reply, &h.mh)
	err := decoder.Decode(&r)
	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("decodeMsgpackToR() error: '%s'", err)))
	}

	VPrintf("decoded type : %T\n", r)
	VPrintf("decoded value: %#v\n", r)

	s := decodeHelper(r, 0, true)
	if s != nil {
		C.Rf_unprotect_ptr(s) // unprotect s before returning it
	}
	return s
}

const FLT_RADIX = 2
const DBL_MANT_DIG = 53

var panicErrIntro = "rmq.decodeHelper detected malformed msgpack panic: "

// new policy: decodeHelper should always return a protected s,
// and the user/client/caller of decodeHelper() is responsible
// for unprotecting s if they are embedding it. This is
// much easier to audit for correctness.
//
// if jsonHeuristicDecode then we'll treat raw []byte that
// start with '{' as JSON and try to decode them too.
//
func decodeHelper(r interface{}, depth int, jsonHeuristicDecode bool) (s C.SEXP) {

	defer func() {
		r := recover()
		if r != nil {
			// truncated or mal-formed msgpack can cause us problems...
			err, isErr := r.(error)
			if !isErr {
				err = fmt.Errorf("'%v'", r)
			}
			C.ReportErrorToR_NoReturn(C.CString(panicErrIntro + err.Error() + "\n" + string(debug.Stack())))
		}
	}()

	VPrintf("decodeHelper() at depth %d, decoded type is %T\n", depth, r)
	switch val := r.(type) {
	case string:
		VPrintf("depth %d found string case: val = %#v\n", depth, val)
		s = C.Rf_mkString(C.CString(val))
		C.Rf_protect(s)
		return s

	case int:
		VPrintf("depth %d found int case: val = %#v\n", depth, val)
		s = C.Rf_ScalarReal(C.double(float64(val)))
		C.Rf_protect(s)
		return s

	case int32:
		VPrintf("depth %d found int32 case: val = %#v\n", depth, val)
		s = C.Rf_ScalarReal(C.double(float64(val)))
		C.Rf_protect(s)
		return s

	case int64:
		VPrintf("depth %d found int64 case: val = %#v\n", depth, val)
		s = C.Rf_ScalarReal(C.double(float64(val)))
		C.Rf_protect(s)
		return s

	case float64:
		VPrintf("depth %d found float64 case: val = %#v\n", depth, val)
		s = C.Rf_ScalarReal(C.double(val))
		C.Rf_protect(s)
		return s

	case []interface{}:
		VPrintf("depth %d found []interface{} case: val = %#v\n", depth, val)

		var sxpTy C.SEXPTYPE = C.VECSXP

		lenval := len(val)
		if lenval == 0 {
			emptyvec := C.allocVector(C.NILSXP, C.R_xlen_t(0))
			C.Rf_protect(emptyvec)
			return emptyvec
		}

		if lenval > 0 {
			first := val[0]
			VPrintf(" ... also at depth %d,   ---> first has type '%T' and value '%v'\n", depth, first, first)

			switch first.(type) {
			case string:
				sxpTy = C.STRSXP

				stringSlice := C.allocVector(sxpTy, C.R_xlen_t(lenval))
				C.Rf_protect(stringSlice)
				for i := range val {
					C.SET_STRING_ELT(stringSlice, C.R_xlen_t(i), C.mkChar(C.CString(val[i].(string))))
				}
				return stringSlice

			case bool:
				sxpTy = C.LGLSXP
				boolSlice := C.allocVector(sxpTy, C.R_xlen_t(lenval))
				C.Rf_protect(boolSlice)
				for i := range val {
					switch val[i].(bool) {
					case true:
						C.set_lglsxp_true(boolSlice, C.ulonglong(i))
					case false:
						C.set_lglsxp_false(boolSlice, C.ulonglong(i))
					}
				}
				return boolSlice

			case int64:
				// we can only realistically hope to preserve 53 bits worth here.
				// todo? unless... can we require bit64 package be available somehow?
				sxpTy = C.REALSXP

				numSlice := C.allocVector(sxpTy, C.R_xlen_t(lenval))
				C.Rf_protect(numSlice)
				size := unsafe.Sizeof(C.double(0))
				naflag := false
				rmax := int64(C.pow(FLT_RADIX, DBL_MANT_DIG) - 1)
				//VPrintf("rmax = %v\n", rmax) //  rmax = 9007199254740991
				rmin := -rmax
				ptrNumSlice := unsafe.Pointer(C.REAL(numSlice))
				var ui uintptr
				var rhs C.double
				for i := range val {
					n := val[i].(int64)
					VPrintf("n = %d, rmax = %d, n > rmax = %v\n", n, rmax, n > rmax)

					if n < rmin || n > rmax {
						naflag = true
					}

					ui = uintptr(i)
					rhs = C.double(float64(n))
					// Try to avoid any gc activity (from the Go runtime) while
					// in the middle of uintptr <-> unsafe.Pointer conversion, as
					// if the gc were to catch us in the middle of that conversion
					// it might crash.
					// Hence we do pointer arithmetic all at once in one expression,
					// which is at present (Oct 2015) is the recommended safe way
					// to do pointer arithmetic in Go. See
					// https://github.com/golang/go/issues/8994 for discussion.
					*((*C.double)(unsafe.Pointer(uintptr(ptrNumSlice) + size*ui))) = rhs
				}
				if naflag {
					C.WarnAndContinue(C.CString("integer precision lost while converting to double"))
				}
				return numSlice

			case float64:
				sxpTy = C.REALSXP

				numSlice := C.allocVector(sxpTy, C.R_xlen_t(lenval))
				C.Rf_protect(numSlice)
				size := unsafe.Sizeof(C.double(0))

				// unfortunately C.memmove() doesn't work here (I tried). I speculate this is because val[i] is
				// really wrapped in an interface{} rather than being a actual float64. val *is* an
				// []interface{} after all.
				var rhs C.double
				ptrNumSlice := unsafe.Pointer(C.REAL(numSlice))
				for i := range val {
					rhs = C.double(val[i].(float64))
					*((*C.double)(unsafe.Pointer(uintptr(ptrNumSlice) + size*uintptr(i)))) = rhs
				}
				return numSlice

			}
		}

		intslice := C.allocVector(sxpTy, C.R_xlen_t(lenval))
		C.Rf_protect(intslice)
		for i := range val {
			elt := decodeHelper(val[i], depth+1, jsonHeuristicDecode)
			C.SET_VECTOR_ELT(intslice, C.R_xlen_t(i), elt)
			C.Rf_unprotect_ptr(elt) // safely inside intslice now
		}
		return intslice

	case map[string]interface{}:

		s = C.allocVector(C.VECSXP, C.R_xlen_t(len(val)))
		C.Rf_protect(s)
		names := C.allocVector(C.VECSXP, C.R_xlen_t(len(val)))
		C.Rf_protect(names)

		VPrintf("depth %d found map[string]interface case: val = %#v\n", depth, val)
		sortedMapKey, sortedMapVal := makeSortedSlicesFromMap(val)
		for i := range sortedMapKey {

			ele := decodeHelper(sortedMapVal[i], depth+1, jsonHeuristicDecode)
			C.SET_VECTOR_ELT(s, C.R_xlen_t(i), ele)
			C.Rf_unprotect_ptr(ele) // unprotect ele now that it is safely inside s.

			ksexpString := C.Rf_mkString(C.CString(sortedMapKey[i]))
			C.Rf_protect(ksexpString)
			C.SET_VECTOR_ELT(names, C.R_xlen_t(i), ksexpString)
			C.Rf_unprotect_ptr(ksexpString) // safely inside names
		}
		C.setAttrib(s, C.R_NamesSymbol, names)
		C.Rf_unprotect_ptr(names) // safely attached to s.

	case []byte:
		VPrintf("depth %d found []byte case: val = %#v\n", depth, val)

		if jsonHeuristicDecode {
			if len(val) > 0 && val[0] == '{' {
				jsonToR := decodeJsonToR(val)
				C.Rf_protect(jsonToR)
				return jsonToR
			}
		}
		rawmsg := C.allocVector(C.RAWSXP, C.R_xlen_t(len(val)))
		C.Rf_protect(rawmsg)
		if len(val) > 0 {
			C.memcpy(unsafe.Pointer(C.RAW(rawmsg)), unsafe.Pointer(&val[0]), C.size_t(len(val)))
		}
		return rawmsg

	case nil:
		s = C.R_NilValue
		C.Rf_protect(s) // must, for uniformly consistency. else we get protect imbalances.
		return s

	case bool:
		boolmsg := C.allocVector(C.LGLSXP, C.R_xlen_t(1))
		C.Rf_protect(boolmsg)
		if val {
			C.set_lglsxp_true(boolmsg, 0)
		} else {
			C.set_lglsxp_false(boolmsg, 0)
		}
		return boolmsg

	default:
		fmt.Printf("unknown type in type switch, val = %#v.  type = %T.\n", val, val)
	}

	return s
}

//export FromMsgpack
//
// FromMsgpack converts a serialized RAW vector of of msgpack2
// encoded bytes into an R object. We use msgpack2 so that there is
// a difference between strings (utf8 encoded) and binary blobs
// which can contain '\0' zeros. The underlying msgpack2 library
// is the awesome https://github.com/ugorji/go/tree/master/codec
// library from Ugorji Nwoke.
//
func FromMsgpack(s C.SEXP) C.SEXP {
	// starting from within R, we convert a raw byte vector into R structures.

	// s must be a RAWSXP
	if C.TYPEOF(s) != C.RAWSXP {
		C.ReportErrorToR_NoReturn(C.CString("from.msgpack(x) requires x be a RAW vector of bytes."))
	}

	n := int(C.Rf_xlength(s))
	if n == 0 {
		return C.R_NilValue
	}
	bytes := make([]byte, n)

	C.memcpy(unsafe.Pointer(&bytes[0]), unsafe.Pointer(C.RAW(s)), C.size_t(n))

	return decodeMsgpackToR(bytes)
}

//export ToMsgpack
//
// ToMsgpack converts an R object into serialized RAW vector
// of msgpack2 encoded bytes. We use msgpack2 so that there is
// a difference between strings (utf8 encoded) and binary blobs
// which can contain '\0' zeros. The underlying msgpack2 library
// is the awesome https://github.com/ugorji/go/tree/master/codec
// library from Ugorji Nwoke.
//
func ToMsgpack(s C.SEXP) C.SEXP {
	byteSlice := encodeRIntoMsgpack(s)

	if len(byteSlice) == 0 {
		return C.R_NilValue
	}
	rawmsg := C.allocVector(C.RAWSXP, C.R_xlen_t(len(byteSlice)))
	C.Rf_protect(rawmsg)
	C.memcpy(unsafe.Pointer(C.RAW(rawmsg)), unsafe.Pointer(&byteSlice[0]), C.size_t(len(byteSlice)))
	C.Rf_unprotect_ptr(rawmsg)

	return rawmsg
}

func encodeRIntoMsgpack(s C.SEXP) []byte {
	iface := SexpToIface(s)

	VPrintf("SexpToIface returned: '%#v'\n", iface)

	if iface == nil {
		return []byte{}
	}

	h.init()

	var w bytes.Buffer
	enc := codec.NewEncoder(&w, &h.mh)
	err := enc.Encode(&iface)
	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("encodeRIntoMsgpack() error: '%s'", err)))
	}

	return w.Bytes()
}

// SexpToIface() does the heavy lifting of converting from
// an R value to a Go value. Initially just a subroutine
// of the internal encodeRIntoMsgpack(), it is also useful
// on its own for doing things like embedding R inside Go.
//
// Currently VECSXP, REALSXP, INTSXP, RAWSXP, and STRSXP
// are supported. In other words, we decode: lists,
// numeric vectors, integer vectors, raw byte vectors,
// string vectors, and recursively defined list elements.
// If list elements are named, the named list is turned
// into a map in Go.
func SexpToIface(s C.SEXP) interface{} {

	n := int(C.Rf_xlength(s))
	if n == 0 {
		return nil // drops type info. Meh.
	}

	switch C.TYPEOF(s) {
	case C.VECSXP:
		// an R generic vector; e.g list()
		VPrintf("encodeRIntoMsgpack sees VECSXP\n")

		// could be a map or a slice. Check out the names.
		rnames := C.Rf_getAttrib(s, C.R_NamesSymbol)
		rnamesLen := int(C.Rf_xlength(rnames))
		VPrintf("namesLen = %d\n", rnamesLen)
		if rnamesLen > 0 {
			myMap := map[string]interface{}{}
			for i := 0; i < rnamesLen; i++ {
				myMap[C.GoString(C.get_string_elt(rnames, C.ulonglong(i)))] = SexpToIface(C.VECTOR_ELT(s, C.R_xlen_t(i)))
			}
			VPrintf("VECSXP myMap = '%#v'\n", myMap)
			return myMap
		} else {
			// else: no names, so we treat it as an array instead of as a map
			mySlice := make([]interface{}, n)
			for i := 0; i < n; i++ {
				mySlice[i] = SexpToIface(C.VECTOR_ELT(s, C.R_xlen_t(i)))
			}
			VPrintf("VECSXP mySlice = '%#v'\n", mySlice)
			return mySlice
		}

	case C.REALSXP:
		// a vector of float64 (numeric)
		VPrintf("encodeRIntoMsgpack sees REALSXP\n")
		mySlice := make([]float64, n)
		for i := 0; i < n; i++ {
			mySlice[i] = float64(C.get_real_elt(s, C.ulonglong(i)))
		}
		VPrintf("VECSXP mySlice = '%#v'\n", mySlice)
		return mySlice

	case C.INTSXP:
		// a vector of int32
		VPrintf("encodeRIntoMsgpack sees INTSXP\n")
		mySlice := make([]int, n)
		for i := 0; i < n; i++ {
			mySlice[i] = int(C.get_int_elt(s, C.ulonglong(i)))
		}
		VPrintf("INTSXP mySlice = '%#v'\n", mySlice)
		return mySlice

	case C.RAWSXP:
		VPrintf("encodeRIntoMsgpack sees RAWSXP\n")
		mySlice := make([]byte, n)
		C.memcpy(unsafe.Pointer(&mySlice[0]), unsafe.Pointer(C.RAW(s)), C.size_t(n))
		VPrintf("RAWSXP mySlice = '%#v'\n", mySlice)
		return mySlice

	case C.STRSXP:
		// a vector of string (pointers to charsxp that are interned)
		VPrintf("encodeRIntoMsgpack sees STRSXP\n")
		mySlice := make([]string, n)
		for i := 0; i < n; i++ {
			mySlice[i] = C.GoString(C.get_string_elt(s, C.ulonglong(i)))
		}
		VPrintf("STRSXP mySlice = '%#v'\n", mySlice)
		return mySlice

	case C.NILSXP:
		// c(); an empty vector
		VPrintf("encodeRIntoMsgpack sees NILSXP\n")
		return nil

	case C.CHARSXP:
		// a single string, interned in a global pool for reuse by STRSXP.
		VPrintf("encodeRIntoMsgpack sees CHARSXP\n")
	case C.SYMSXP:
		VPrintf("encodeRIntoMsgpack sees SYMSXP\n")
	case C.LISTSXP:
		VPrintf("encodeRIntoMsgpack sees LISTSXP\n")
	case C.CLOSXP:
		VPrintf("encodeRIntoMsgpack sees CLOSXP\n")
	case C.ENVSXP:
		VPrintf("encodeRIntoMsgpack sees ENVSXP\n")
	case C.PROMSXP:
		VPrintf("encodeRIntoMsgpack sees PROMSXP\n")
	case C.LANGSXP:
		VPrintf("encodeRIntoMsgpack sees LANGSXP\n")
	case C.SPECIALSXP:
		VPrintf("encodeRIntoMsgpack sees SPECIALSXP\n")
	case C.BUILTINSXP:
		VPrintf("encodeRIntoMsgpack sees BUILTINSXP\n")
	case C.LGLSXP:
		mySlice := make([]bool, n)
		for i := 0; i < n; i++ {
			switch C.get_lglsxp(s, C.ulonglong(i)) {
			case 0:
				mySlice[i] = false
			case 1:
				mySlice[i] = true
			}
		}
		VPrintf("LGLSXP mySlice = '%#v'\n", mySlice)
		return mySlice
	case C.CPLXSXP:
		VPrintf("encodeRIntoMsgpack sees CPLXSXP\n")
	case C.DOTSXP:
		VPrintf("encodeRIntoMsgpack sees DOTSXP\n")
	case C.ANYSXP:
		VPrintf("encodeRIntoMsgpack sees ANYSXP\n")
	case C.EXPRSXP:
		VPrintf("encodeRIntoMsgpack sees EXPRSXP\n")
	case C.BCODESXP:
		VPrintf("encodeRIntoMsgpack sees BCODESXP\n")
	case C.EXTPTRSXP:
		VPrintf("encodeRIntoMsgpack sees EXTPTRSXP\n")
	case C.WEAKREFSXP:
		VPrintf("encodeRIntoMsgpack sees WEAKREFSXP\n")
	case C.S4SXP:
		VPrintf("encodeRIntoMsgpack sees S4SXP\n")
	default:
		VPrintf("encodeRIntoMsgpack sees <unknown>\n")
	}
	VPrintf("... warning: encodeRIntoMsgpack() ignoring this input.\n")

	return nil
}

//msgp:ignore mapsorter KiSlice

type mapsorter struct {
	key   string
	iface interface{}
}

type KiSlice []*mapsorter

func (a KiSlice) Len() int           { return len(a) }
func (a KiSlice) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a KiSlice) Less(i, j int) bool { return a[i].key < a[j].key }

func makeSortedSlicesFromMap(m map[string]interface{}) ([]string, []interface{}) {
	key := make([]string, len(m))
	val := make([]interface{}, len(m))
	so := make(KiSlice, 0)
	for k, i := range m {
		so = append(so, &mapsorter{key: k, iface: i})
	}
	sort.Sort(so)
	for i := range so {
		key[i] = so[i].key
		val[i] = so[i].iface
	}
	return key, val
}

//export ReadMsgpackFrame
//
// ReadMsgpackFrame reads the msgpack frame at byteOffset in rawStream, decodes the
// 2-5 bytes of a msgpack binary array (either bin8, bin16, or bin32), and returns
// and the decoded-into-R object and the next byteOffset to use.
//
func ReadMsgpackFrame(rawStream C.SEXP, byteOffset C.SEXP) C.SEXP {

	var start int
	if C.TYPEOF(byteOffset) == C.REALSXP {
		start = int(C.get_real_elt(byteOffset, 0))
	} else if C.TYPEOF(byteOffset) == C.INTSXP {
		start = int(C.get_int_elt(byteOffset, 0))
	} else {
		C.ReportErrorToR_NoReturn(C.CString("read.msgpack.frame(x, byteOffset) requires byteOffset to be a numeric byte-offset number."))
	}

	// rawStream must be a RAWSXP
	if C.TYPEOF(rawStream) != C.RAWSXP {
		C.ReportErrorToR_NoReturn(C.CString("read.msgpack.frame(x, byteOffset) requires x be a RAW vector of bytes."))
	}

	n := int(C.Rf_xlength(rawStream))
	if n == 0 {
		return C.R_NilValue
	}

	if start >= n {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("read.msgpack.frame(x, byteOffset) error: byteOffset(%d) is beyond the length of x (x has len %d).", start, n)))
	}

	var decoder [5]byte
	C.memcpy(unsafe.Pointer(&decoder[0]), unsafe.Pointer(C.get_raw_elt_ptr(rawStream, C.ulonglong(start))), C.size_t(5))
	headerSz, _, totalSz, err := DecodeMsgpackBinArrayHeader(decoder[:])
	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("ReadMsgpackFrame error trying to decode msgpack frame: %s", err)))
	}

	if start+totalSz > n {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("read.msgpack.frame(x, byteOffset) error: byteOffset(%d) plus the frames size(%d) goes beyond the length of x (x has len %d).", start, totalSz, n)))
	}

	bytes := make([]byte, totalSz)
	C.memcpy(unsafe.Pointer(&bytes[0]), unsafe.Pointer(C.get_raw_elt_ptr(rawStream, C.ulonglong(start))), C.size_t(totalSz))

	rObject := decodeMsgpackToR(bytes[headerSz:])
	C.Rf_protect(rObject)
	returnList := C.allocVector(C.VECSXP, C.R_xlen_t(2))
	C.Rf_protect(returnList)
	C.SET_VECTOR_ELT(returnList, C.R_xlen_t(0), C.Rf_ScalarReal(C.double(float64(start+totalSz))))
	C.SET_VECTOR_ELT(returnList, C.R_xlen_t(1), rObject)
	C.Rf_unprotect_ptr(rObject)
	C.Rf_unprotect_ptr(returnList)
	return returnList
}

// DecodeMsgpackBinArrayHeader parses the first 2-5 bytes of a msgpack-format serialized binary array
// and returns the headerSize, payloadSize and totalFramesize for the frame the starts at p[0].
func DecodeMsgpackBinArrayHeader(p []byte) (headerSize int, payloadSize int, totalFrameSize int, err error) {
	lenp := len(p)

	switch p[0] {
	case 0xc4: // msgpackBin8
		if lenp < 2 {
			err = fmt.Errorf("DecodeMsgpackBinArrayHeader error: p len (%d) too small", lenp)
			return
		}
		headerSize = 2
		payloadSize = int(p[1])
	case 0xc5: // msgpackBin16
		if lenp < 3 {
			err = fmt.Errorf("DecodeMsgpackBinArrayHeader error: p len (%d) too small", lenp)
			return
		}
		headerSize = 3
		payloadSize = int(binary.BigEndian.Uint16(p[1:3]))
	case 0xc6: // msgpackBin32
		if lenp < 5 {
			err = fmt.Errorf("DecodeMsgpackBinArrayHeader error: p len (%d) too small", lenp)
			return
		}
		headerSize = 5
		payloadSize = int(binary.BigEndian.Uint32(p[1:5]))
	}

	totalFrameSize = headerSize + payloadSize
	return
}

//export ReadNewlineDelimJson
//
// ReadNewlineDelimJson reads a json object at byteOffset in rawStream, expects
// it to be newline terminated, and returns the
// decoded-into-R object and the next byteOffset to use (the byte just after
// the terminating newline).
//
func ReadNewlineDelimJson(rawStream C.SEXP, byteOffset C.SEXP) C.SEXP {
	C.Rf_protect(rawStream)

	var start int
	if C.TYPEOF(byteOffset) == C.REALSXP {
		start = int(C.get_real_elt(byteOffset, 0))
	} else if C.TYPEOF(byteOffset) == C.INTSXP {
		start = int(C.get_int_elt(byteOffset, 0))
	} else {
		C.ReportErrorToR_NoReturn(C.CString("read.ndjson(x, byteOffset) requires byteOffset to be a numeric byte-offset number."))
	}
	// rawStream must be a RAWSXP
	if C.TYPEOF(rawStream) != C.RAWSXP {
		C.ReportErrorToR_NoReturn(C.CString("read.ndjson(x, byteOffset) requires x be a RAW vector of bytes."))
	}

	n := int(C.Rf_xlength(rawStream))
	if n == 0 {
		return C.R_NilValue
	}

	if start >= n {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("read.ndjson(x, byteOffset) error: byteOffset(%d) is at or beyond the length of x (x has len %d).", start, n)))
	}
	// INVAR: start < n

	// find the next newline or end of raw array
	next := int(C.next_newline_pos(rawStream, C.ulonglong(start+1), C.ulonglong(n)))
	totalSz := next - start

	bytes := make([]byte, totalSz)
	fromPtr := unsafe.Pointer(C.get_raw_elt_ptr(rawStream, C.ulonglong(start)))
	C.memcpy(unsafe.Pointer(&bytes[0]), fromPtr, C.size_t(totalSz))
	rObject := decodeJsonToR(bytes)
	C.Rf_protect(rObject)
	returnList := C.allocVector(C.VECSXP, C.R_xlen_t(2))
	C.Rf_protect(returnList)
	C.SET_VECTOR_ELT(returnList, C.R_xlen_t(0), C.Rf_ScalarReal(C.double(float64(start+totalSz))))
	C.SET_VECTOR_ELT(returnList, C.R_xlen_t(1), rObject)
	C.Rf_unprotect_ptr(rObject)
	C.Rf_unprotect_ptr(returnList)
	C.Rf_unprotect_ptr(rawStream)
	return returnList
}

// return an un-protected C.SEXP, like decodeMsgpackToR does;
// we need this symmetry since one can call the other now.
func decodeJsonToR(reply []byte) C.SEXP {

	h.init()
	var r interface{}

	decoder := codec.NewDecoderBytes(reply, &h.jh)
	err := decoder.Decode(&r)
	if err != nil {
		C.ReportErrorToR_NoReturn(C.CString(fmt.Sprintf("read.ndjson() error: '%s'", err)))
	}

	VPrintf("decoded type : %T\n", r)
	VPrintf("decoded value: %#v\n", r)

	s := decodeHelper(r, 0, true)
	if s != nil {
		C.Rf_unprotect_ptr(s) // unprotect s before returning it
	}
	return s
}

// for debugging an unprotected variable s, call chk(s)
func chk(s C.SEXP) {
	if 0 == C.Rf_isProtected(s) {
		panic(fmt.Sprintf("\n\n\n !!! what the heck: s (%p) is not protected!\n\n", s))
	}
}
