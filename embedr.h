// -*- mode: C++; c-indent-level: 2; c-basic-offset: 2; tab-width: 8 -*-
///////////////////////////////////////////////////////////////////////////
// Copyright (C) 2014 Jason E. Aten                                      //
// License: Apache 2.0.                                                  //
// http://www.apache.org/licenses/                                       //
///////////////////////////////////////////////////////////////////////////


#ifndef INTERFACE_HPP
#define INTERFACE_HPP

#include <R.h>
#include <Rinternals.h>
#include <Rinterface.h>
#include <Rmath.h> // for RBoolean TRUE / FALSE
#include <R_ext/Rdynload.h>
#include <R_ext/Utils.h>
#include <R_ext/Parse.h>
#include <R_ext/Callbacks.h>
//include <R_ext/GraphicsDevice.h> // has R_interrupts_pending but won't compile atm.
#include <Rembedded.h>
#include <signal.h>

// access the gnu readline's history directly
// rather than having to write it to a file each time.
#include <readline/readline.h>
#include <readline/history.h>

#ifdef __cplusplus
extern "C" {
#endif

  // from main.c for simulating ctrl-c. R sets this to 1 on receiving ctrl-c.
  extern int R_interrupts_pending;
  
  // readline changes signal handlers all the time, without setting
  // SA_ONSTACK, which means risk of Go noticing and panicing. Try
  // to avoid readline.
  // extern Rboolean UsingReadline; // undefined reference arg.
  
  // the last successfully evaluated expression from the Toplevel callback
  // The Go code should free() this after copying it, as it was made with
  // strdup(), and then set it to 0;
  // defined in cpp/embedr.cpp
  extern char* lastSucessExpression;

  // Any customPrompt can be at most
  // 99 characters; If we set it, we own the memory.
  // So either point to a static string, or take
  // responsibility for malloc and free of it.
  // In particular, remember to manage memory
  // if you need to when you change the
  // custom prompt.
  //
  // Should typically end in "> " which is
  // the default R prompt.
  extern char* customPrompt;
  
  // ditto for last value of the last expression.
  //extern char* lastValue;
  
  // Not in the usualy .h, so how are we to get to them?
  // from src/main/deparse.c
  //      src/include/Defn.h
  SEXP Rf_deparse1line_(SEXP call, Rboolean abbrev, int opts);
  SEXP Rf_deparse1line(SEXP call, Rboolean abbrev);
  SEXP Rf_deparse1(SEXP call, Rboolean abbrev, int opts);

  // in src/main/options.c:204 v4.1.0
  SEXP SetOption(SEXP tag, SEXP value);
  
int Rf_isProtected(SEXP s); // debugging utility from R-3.x.x/src/main/memory.c
  
unsigned long int get_starting_signint_handler();
unsigned long int get_signint_handler();

  void restore_all_starting_signal_handlers();  
  extern struct sigaction starting_act[NSIG];

  void set_SA_ONSTACK();
  extern struct sigaction setsa_act[NSIG];


  void record_sigaction_to_current_act(); // write to current_act
  void restore_sigaction_from_current_act(); // restore from current_act
  extern struct sigaction current_act[NSIG];
  
  void record_sigaction_to_my_r_act();
  void restore_sigaction_from_my_r_act();
  extern struct sigaction my_r_act[NSIG];

  void null_out_all_signal_handlers();
  void record_sigaction_to_current_act_and_null_out();

  int wrapReplDLLdo1();  
  
  SEXP rmq(SEXP name_);
  
  //  int JasonsLinkeMe();
  
  void ReportErrorToR_NoReturn(const char* msg);

  void PrintToR(const char* msg);

  void SetTypeToLANGSXP(SEXP* sexp);
  
  void WarnAndContinue(const char* msg);

  const char* get_string_elt(SEXP x, unsigned long long i);

  double get_real_elt(SEXP x, unsigned long long i);

  int get_int_elt(SEXP x, unsigned long long i);

  void set_lglsxp_true(SEXP lgl, unsigned long long i);
  void set_lglsxp_false(SEXP lgl, unsigned long long i);
  int get_lglsxp(SEXP lgl, unsigned long long i);

  // locate the next newline character in the raw array,
  // starting at beg, and up to but not including endx.
  // If not found, will return endx.
  unsigned long long next_newline_pos(SEXP raw, unsigned long long beg, unsigned long long endx);
  
  unsigned char* get_raw_elt_ptr(SEXP raw, unsigned long long i);  

  // Must only call one or other of the init functions; and only once.  
  void callInitEmbeddedR();

  // Must only call one or other of the init functions; and only once.  
  void callInitEmbeddedREPL();
  
  // PRE: callInitEmbeddedR() has been called exactly once before entering.
  // IMPORTANT: caller must PROTECT the returned SEXP and unprotect when done. Unless it is R_NilValue.
  SEXP callParseEval(const char* evalme, int* evalErrorOccurred);

  void callEndEmbeddedR();

  long RegisterMyEmbedrToplevelCallback();

  void GoCleanupFunc();
  void RCallbackToGoFunc();
  void RCallbackToGoFuncDvv();
  
  void restore_all_starting_signal_handlers_WITH_SA_ONSTACK();

  // uses callParseEval, so the R interpreter must have be initialized.
  void injectCustomPrompt();

  // try to emulate ctrl-c in a safe way (without crashing via cgo).
  void setR_interrupts_pending();

  // get the last history line from R via readline's history lib
  char* last_history();

  // try to setup C handling of ctrl-c directly without Go intermediating.
  void sig_handler_for_sigint_R(int signo);
  
#ifdef __cplusplus
}
#endif


#endif // INTERFACE_HPP
