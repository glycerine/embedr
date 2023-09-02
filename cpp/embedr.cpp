///////////////////////////////////////////////////////////////////////////
// Copyright (C) 2014 Jason E. Aten                                      //
// License: Apache 2.0.                                                  //
// http://www.apache.org/licenses/                                       //
///////////////////////////////////////////////////////////////////////////

#include <stdint.h>
#include <stdio.h>
#include <string>
#include <sstream>
#include <stdexcept>
#include <string.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h> // bzero
#include <signal.h>

#include "../embedr.h"


// the last successfully evaluated expression from the Toplevel callback
// The Go code should free() this after copying it, as it was made with
// strdup(), and then set it to 0;
char* lastSucessExpression = 0;

// If the client has set a custom prompt here, then show it.
// custom prompt cannot be larger than 99 bytes.
// Client should malloc it; and free if they replace with a new one.
char* customPrompt = 0;

char setPromptScript[128]; // options("prompt"="customPrompt")

char promptScriptPrefix[] = "options(\"prompt\"=\"";


#ifdef __cplusplus
extern "C" {
#endif

  /* I think these are only for when R is the host. Not when Go is the host.
  // called after the ((constructor)) routines.
  void R_init_embedr(DllInfo *info)
  {
    // Register routines, allocate resources.
    printf("   R_init_embedr() called\n");
  }
  
  // called when R wants to unload.
  void R_unload_embedr(DllInfo *info)
  {
    // Release resources.
    printf("   R_unload_embedr() called\n");
  }
  */
  
  struct sigaction starting_act[NSIG];

  // the ((constructor)) annotation makes my_init() get
  // called before the cshared go routine runtime initializes,
  // which is why we manipulate the signal handling table here.
  // 
  // By setting SIGINT to be SIG_IGN, the go runtime won't take
  // it over. Then we can safely restore the R handlers for
  // for SIGINT and all other signals once the go runtime as
  // completed initialization. The go routine won't ever
  // see any signals, which is good since when embedded as
  // a cshared library, it still doesn't play nicely with
  // the host process (https://github.com/golang/go/issues/13034)
  // as of October 2015/ go1.5.1.
  //
  // See the init() function in rmq.go for the logic that
  // restores the signal handlers.
  //
  void __attribute__ ((constructor)) my_init(void) {
    // When Go is the host, and R the guest DLL/.so,
    // this runs before the R runtime is initialized.
    // Hence we cannot call PrintToR or we will seg-fault
    // immediately.
    // NO!: PrintToR("interface.cpp: my_init() top.\n"); 

    //R_SignalHandlers = 0; // no chance
    //printf("embedr.cpp:79 my_init() top.\n");
    
    // We run in 2 scenarios now (code is re-used):
    // a) Go as host, and we link in the dfs/rmqcb/pkg/embedr
    //    package. At Go host init() time, this here
    //    my_init() will be called, and the Go runtime
    //    sigaction's will be in place. We should not
    //    touch them. But perhaps record them.
    //
    // b) Go as host, the R started up (the above happens),
    //    then R loading in the rmqcb DLL (.so) which contains a 2nd Go
    //    runtime. Now my_init is run during DLL load
    //    by R. Since R's sigaction is NOT in place to
    //    begin with (because (a) already restored Go's
    //    sigaction, we don't really neeed/want to do
    //    any new setting of sigactions (at least at the moment).
    //
    // So we'll leave on the recording into starting_act,
    // but not do any other alterations.
    for (int i = 0; i < NSIG; i++) {
      sigaction(i, NULL, &starting_act[i]); // read them.
      
      // https://pkg.go.dev/os/signal says about calling into CGO code:
      //
      // "If the non-Go code installs any signal handlers,
      // it must use the SA_ONSTACK flag with sigaction.
      // Failing to do so is likely to cause the program
      // to crash if the signal is received. Go programs
      // routinely run with a limited stack, and
      // therefore set up an alternate signal stack."
      //
      // So we will set the flag and write them back out,
      // if they have a handler:
      
      if (starting_act[i].sa_handler != NULL) {
        
        //printf("embedr.cpp:113 before modifying, starting_act[%d].sa_flags = %d\n", i, starting_act[i].sa_flags);
        /*
        starting_act[i].sa_flags = starting_act[i].sa_flags | SA_ONSTACK;
        printf("interface.cpp:60 _after_ modifying, starting_act[%d].sa_flags = %d\n", i, starting_act[i].sa_flags);      

        sigaction(i, &starting_act[i], NULL); // write back each in turn.
      
        printf("interface.cpp:60 starting_act[%d] = %ld\n", i, (unsigned long int)(starting_act[i].sa_handler));
        */
      }
      
    }

    /*
    struct sigaction act_with_ignore_sigint;
    act_with_ignore_sigint.sa_handler = SIG_IGN;
    act_with_ignore_sigint.sa_flags = act_with_ignore_sigint.sa_flags | SA_ONSTACK;
    sigaction(SIGINT, &act_with_ignore_sigint, NULL);
    */
  }

  void restore_all_starting_signal_handlers() {
    for (int i = 0; i < NSIG; i++) {
      sigaction(i, &starting_act[i], NULL);
    }
  }

  void restore_all_starting_signal_handlers_WITH_SA_ONSTACK() {
    //printf("top of restore_all_starting_signal_handlers_WITH_SA_ONSTACK()\n");
    for (int i = 0; i < NSIG; i++) {
      if (starting_act[i].sa_handler != NULL) {
        starting_act[i].sa_flags = starting_act[i].sa_flags | SA_ONSTACK;
        sigaction(i, &starting_act[i], NULL); // write back each in turn.
        //printf("restored orig Go sig handler with SA_ONSTACK for %d\n", i);
      }
    }
  }

  // we want to make sure no Go signal handlers are ever called;
  // so restore all signal handling to newborn process defaults.
  void null_out_all_signal_handlers() {
    // man sigaction: "A signal-specific default action may be reset by setting sa_handler to SIG_DFL."

    struct sigaction defaulter;
    bzero(&defaulter, sizeof(defaulter));
    defaulter.sa_handler = SIG_DFL; // defined as 0/null, but anyway, be future proof.
        
    for (int i = 0; i < NSIG; i++) {      
      if (i == SIGKILL || i ==  SIGSTOP) {
           // these cannot be set, will give error, so do not bother.
        } else {
           sigaction(i, &defaulter, NULL);
      }
    }
  }

  // common combo, avoid one more cgo boundary crossing.
  void record_sigaction_to_current_act_and_null_out() {
    record_sigaction_to_current_act();
    null_out_all_signal_handlers();
  }
  
  
  struct sigaction setsa_act[NSIG];
  
  void set_SA_ONSTACK() {
    for (int i = 0; i < NSIG; i++) {
      sigaction(i, NULL, &setsa_act[i]); // read them.
      if (setsa_act[i].sa_handler != NULL) {
        
        //printf("set_SA_ONSTACK(): before modifying, setsa_act[%d].sa_flags = %d\n", i, setsa_act[i].sa_flags);
        setsa_act[i].sa_flags = setsa_act[i].sa_flags | SA_ONSTACK;
        //printf("set_SA_ONSTACK(): _after_ modifying, setsa_act[%d].sa_flags = %d\n", i, setsa_act[i].sa_flags);      

        sigaction(i, &setsa_act[i], NULL); // write back each in turn.
      }
    }
  }


  struct sigaction current_act[NSIG];
  
  void record_sigaction_to_current_act() {
    for (int i = 0; i < NSIG; i++) {
      sigaction(i, NULL, &current_act[i]); // read them.

      //if (current_act[i].sa_handler != NULL) {
      //printf("record cur:  current_act[%d].sa_handler was not null.\n", i);
      //}      
    }
  }
  
  void restore_sigaction_from_current_act() {
    //printf("top of restore_sigaction_from_current_act()\n");
    for (int i = 0; i < NSIG; i++) {
      // make sure Go runtime will be happy if at all possible.
      current_act[i].sa_flags = current_act[i].sa_flags | SA_ONSTACK;
      
      //if (current_act[i].sa_handler != NULL) {
        //printf("restore cur:  current_act[%d].sa_flags = %d\n", i, current_act[i].sa_flags);
      //}
      sigaction(i, &current_act[i], NULL); // write them.
    }
  }

  struct sigaction my_r_act[NSIG];
  
  void record_sigaction_to_my_r_act() {
    for (int i = 0; i < NSIG; i++) {
      sigaction(i, NULL, &my_r_act[i]); // read them.
    }
  }
  
  void restore_sigaction_from_my_r_act() {
    for (int i = 0; i < NSIG; i++) {
      // maybe?
      my_r_act[i].sa_flags = my_r_act[i].sa_flags | SA_ONSTACK;
      
      sigaction(i, &my_r_act[i], NULL); // write them.
    }
  }
  
  
  unsigned long int get_starting_signint_handler() {
    return (unsigned long int)(starting_act[SIGINT].sa_handler);
  }
  
  unsigned long int get_signint_handler() {
    struct sigaction act;
    sigaction(SIGINT, NULL, &act);    
    return (unsigned long int)(act.sa_handler);
  }
    

  void ReportErrorToR_NoReturn(const char* msg) {
    Rf_error(msg);
  }
  
  void PrintToR(const char* msg) {
    REprintf(msg);
  }
  
  void WarnAndContinue(const char* msg) {
    warning(msg);
  }
  
  void SetTypeToLANGSXP(SEXP* sexp) {
    SET_TYPEOF(*sexp, LANGSXP);
  }

  const char* get_string_elt(SEXP x, unsigned long long i) {
    return CHAR(STRING_ELT(x, i));
  }

  double get_real_elt(SEXP x, unsigned long long i) {
    return REAL(x)[i];
  }


  int get_int_elt(SEXP x, unsigned long long i) {
    return INTEGER(x)[i];
  }

  void set_lglsxp_true(SEXP lgl, unsigned long long i) {
    LOGICAL(lgl)[i] = 1;
  }
  
  void set_lglsxp_false(SEXP lgl, unsigned long long i) {
    LOGICAL(lgl)[i] = 0;
  }

  int get_lglsxp(SEXP lgl, unsigned long long i) {
    return LOGICAL(lgl)[i];
  }

  unsigned char* get_raw_elt_ptr(SEXP raw, unsigned long long i) {  
    return &(RAW(raw)[i]);
  }

  // locate the next newline character in the raw array,
  // starting at beg, and up to but not including endx.
  // If not found, will return endx.
  unsigned long long next_newline_pos(SEXP raw, unsigned long long beg, unsigned long long endx) {  
    unsigned long long i;
    for (i = beg; i < endx; i++) {
      if (RAW(raw)[i] == '\n') {
        return i;
      }
    }
    return endx;
  }


  void CallGoCleanupFunc() {
    GoCleanupFunc();
  }

  void CallRCallbackToGoFunc() {
    RCallbackToGoFunc();
  }
  
  void CallRCallbackToGoFuncDvv() {
    RCallbackToGoFuncDvv();
  }
  
  // register CallGoCleanupFunc
  static const R_CMethodDef cMethods[] = {
                                          {"CallGoCleanupFunc", (DL_FUNC) &CallGoCleanupFunc, 0, NULL},
                                          {"CallRCallbackToGoFunc", (DL_FUNC) &CallRCallbackToGoFunc, 0, NULL},
                                          {"CallRCallbackToGoFuncDvv", (DL_FUNC) &CallRCallbackToGoFuncDvv, 0, NULL},
                                          {NULL, NULL, 0, NULL}
  };
  
  // Must only call one or other of the init functions; and only once.
  void callInitEmbeddedREPL() {

    // https://cran.r-project.org/doc/manuals/R-exts.html#Threading-issues says:
    //
    // You may also want to consider how signals are handled: R sets signal handlers 
    // for several signals, including SIGINT, SIGSEGV, SIGPIPE, SIGUSR1 and SIGUSR2, but these 
    // can all be suppressed by setting the variable R_SignalHandlers (declared in Rinterface.h) to 0.
    R_SignalHandlers = 0; // strange. had no effect. but we aren't calling the main loop.
    // Also, crazy readline calls sigaction every line read. Turn it off. Arg. We could not without making the R command line unusable.

    //char *my_argv[]= {(char*)"repl", (char*)"--no-readline"};
    char *my_argv[]= {(char*)"repl"};
    Rf_initEmbeddedR(sizeof(my_argv)/sizeof(my_argv[0]), my_argv);

    DllInfo *info = R_getEmbeddingDllInfo();
    //R_registerRoutines(info, cMethods, callMethods, NULL, NULL);
    R_registerRoutines(info, cMethods, NULL, NULL, NULL);

    set_SA_ONSTACK();

  }

  // Must only call one or other of the init functions; and only once.  
  void callInitEmbeddedR() {

    R_SignalHandlers = 0;
    // designed to be as quiet as possible, when really embedded.
    //char *my_argv[]= {(char*)"r.embedded.in.golang", (char*)"--silent", (char*)"--vanilla", (char*)"--slave", (char*)"--no-readline"};
    char *my_argv[]= {(char*)"r.embedded.in.golang", (char*)"--silent", (char*)"--vanilla", (char*)"--slave"};
    Rf_initEmbeddedR(sizeof(my_argv)/sizeof(my_argv[0]), my_argv);

    set_SA_ONSTACK();
  }

  // PRE: callInitEmbeddedR() has been called exactly once before entering.
  // IMPORTANT: caller must PROTECT the returned SEXP and unprotect when done. Unless it is R_NilValue.
  // call set_SA_ONSTACK() at beginning and end:
  // try to prevent Go seeing signal handlers without ONSTACK, since
  // the go runtime will panic. Apparently R is setting signal handlers anew
  // all the time. Arg!
  SEXP callParseEval(const char* evalme, int* evalErrorOccured) {
    SEXP ans,expression, myCmd;
    set_SA_ONSTACK();
    
    // null(0) would ensure no error is ever reported.
    // evalErrorOccured = 0;
    ParseStatus parseStatusCode;

    PROTECT(myCmd = mkString(evalme));

    /* PARSE_NULL will not be returned by R_ParseVector 
       typedef enum {
       PARSE_NULL,
       PARSE_OK,
       PARSE_INCOMPLETE,
       PARSE_ERROR,
       PARSE_EOF
       } ParseStatus;
    */
    PROTECT(expression = R_ParseVector(myCmd, 1, &parseStatusCode, R_NilValue));
    if (parseStatusCode != PARSE_OK) {
      UNPROTECT(2);

      set_SA_ONSTACK();  
      return R_NilValue;
    }

    ans = R_tryEval(VECTOR_ELT(expression,0), R_GlobalEnv, evalErrorOccured);
    UNPROTECT(2);
    // evalErrorOccured will be 1 if an error happened, and ans will be R_NilValue

    set_SA_ONSTACK();    
    return ans; // caller must protect and unprotect when done
  }

  void callEndEmbeddedR() {
    Rf_endEmbeddedR(0);
  }

  // Unrelated to Task Callbacks.
  SEXP CallbackToHandler(SEXP handler_, SEXP arg_, SEXP rho_) {
    SEXP evalres;
    
    SEXP R_fcall, msg;
    if(!isFunction(handler_)) error("‘handler’ must be a function");
    if(!isEnvironment(rho_)) error("‘rho’ should be an environment. e.g. new.env()");
    
    PROTECT(R_fcall = lang2(handler_, arg_));
    PROTECT(evalres = eval(R_fcall, rho_));
    UNPROTECT(2);
    return evalres;
  }

  // This works for the callbacks in a R_ReplDLLinit() / R_ReplDLLdo1() loop
  // and the registration can happen before the R_ReplDLLinit(), but
  // no callbacks will happen on simple DLL Rf_Eval()s, only on the
  // R_ReplDLLdo1() that are used in embedr.go SimpleREPL() to build
  // a REPL loop.
  // 
  // MyEmbedrToplevelCallback is an example of a function that is
  // registered using Rf_addTaskCallback() to get called after
  // each toplevel R action.
  //
  // Registered TaskCallbacks get called from R's main loop
  // after each toplevel task (i.e. each expression evaluated at the toplevel).
  //
  // If they return FALSE then they are deregistered and not called again.
  // If they return TRUE then they will be called after the next toplevel
  // task completes.
  //
  Rboolean MyEmbedrToplevelCallback(SEXP expr,
                                    SEXP value,
                                    Rboolean succeeded,
                                    Rboolean visible,
                                    void * data) {

    //PrintToR("embedr.cpp: MyEmbedrToplevelCallback() has been called!\n");     
    
    //printf("MyEmbedrTopLevelCallback has been called; The previous expression suceeded = %d;\n", succeeded);
    //fprintf(stderr, "On stderr: MyEmbedrTopLevelCallback has been called; returning FALSE to deregister ourselves! The previous expression suceeded = %d; callcount = %d\n", succeeded, callcount);
    if (succeeded) {
      
      const char* x = CHAR(STRING_ELT(Rf_deparse1line(expr, FALSE), 0));

      // if not already freed, don't leak now.
      // free(0) is a no-op. Go side should
      // 0 the pointer after reading it.
      free(lastSucessExpression); 
      lastSucessExpression = strdup(x);
      //printf("MyEmbedrToplevelCallback: succeeded: '%s'\n", x);

      // This works but gives the raw data rather than
      //  the nicely formatted output we usually desire
      //  from the presentation by the print commands.
      //  Since it also can take up alot of memory to
      //  store a big string representation, and it won't
      //  be often used, we'll turn this off for now.
      //
      // ditto for last value of the last expression.
      //const char* v = CHAR(STRING_ELT(Rf_deparse1line(value, FALSE), 0));
      //free(lastValue); 
      //lastValue = strdup(v);      
      //printf("MyEmbedrToplevelCallback: last value: '%s'\n", v);
    }

    if (customPrompt == NULL) {
      //printf("customPrompt was NULL\n");
    } else {
      injectCustomPrompt();
    }
    
    // keep calling us for each toplevel, FALSE would unregister the callback.
    return TRUE;
  }

  void injectCustomPrompt() {
    
      if (customPrompt == NULL) {
          //printf("customPrompt was NULL\n");
          return;
      }
    
      // Could not figure out how to call SetOption
      // from C, so use an R script. Build the
      // script in the static character array
      // setPromptScript.
      //  i.e. we create text saying:
      //
      // `options("prompt" = "` + customPrompt + `")`
      //
      // where customPrompt has been filled in by
      // the user of this API.
      
      size_t n = strlen(customPrompt); // does not count the trail 0.

      //printf("customPrompt is size %d: '%s'\n", n, customPrompt);
      if (n > 0) {
        
          if (n > 99) {
              n = 99; // stay inside our setPromptScript static array buffer size.
          }

          // always zero terminated because script is a little
          // bigger than the prefix + the max 99 characters.
          bzero(setPromptScript, sizeof(setPromptScript));

          // Our setPromptScript is statically 128 characters. This
          // should be enough because:
          // 123456789012345678 => 18 char + 99 custom + 3 = 120 including terminator.
          // options("prompt"="
          strcpy(&setPromptScript[0], promptScriptPrefix); 
          
          size_t pre = strlen(setPromptScript);
          strncpy(&setPromptScript[pre], customPrompt, n);
          
          pre = strlen(setPromptScript);
          strcpy(&setPromptScript[pre], "\")"); // 3 more, including terminating 0.
          
          int evalerr = 0;
          callParseEval(setPromptScript, &evalerr);
          //printf("tried to execute script: '%s'; got evalerr = %d;\n", setPromptScript, evalerr);
          
      } // end if n > 0
  }
  
  long RegisterMyEmbedrToplevelCallback() {
    int mynum = 0;
    Rf_addTaskCallback(MyEmbedrToplevelCallback, NULL, NULL, "MyEmbedrToplevelCallback", &mynum);
    return long(mynum);
  }

  void setR_interrupts_pending() {
    R_interrupts_pending = 1;
  }
  
#ifdef __cplusplus
}
#endif

 
