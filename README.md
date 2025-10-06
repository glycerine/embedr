embedr: embed R inside a Go (golang) program.
=========

embedr is a small library for embedding the R (rproject.org) statistical
engine in a Go (golang) program.

Developed under MacOS and Linux. It is unlikely to work on Windows out of the box.

~~~
latest build tactic, 
to get libR.a (grrr: seems  --enable-R-shlib forbids making a libR.a !?!)

FC=/usr/bin/gfortran-11  LDFLAGS="-Wl,--export-dynamic" ./configure  --enable-static LDFLAGS="-Wl,--export-dynamic"  --enable-R-static-lib

but this probably suffices to get libR.a
./configure  --enable-static --enable-R-static-lib

jaten@rog ~/glycerine/embedr (master) $ nm -C /usr/local/lib/R/lib/libR.so | grep Rf_addTaskCallback
000000000018ad00 t Rf_addTaskCallback <<< problem: not exported
jaten@rog ~/glycerine/embedr (master) $ 

jaten@rog /usr/local/src/R-4.5.1 $ nm -C /usr/local/lib/R/lib/libR.a |grep Rf_addTaskCallback 
00000000000032c6 T Rf_addTaskCallback  <<< static means always exported, maybe?
jaten@rog /usr/local/src/R-4.5.1 $ 

~~~

------------

Copyright (C) 2024 by Jason E. Aten, Ph.D.

Licence: MIT

