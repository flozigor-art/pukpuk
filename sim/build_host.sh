#!/bin/sh
# Builds ./jsfxrun: runs a JSFX file on raw float64 stereo, using ysfx (EEL2 from WDL).
set -e
cd "$(dirname "$0")"
[ -d ysfx ] || git clone --depth 1 --recurse-submodules --shallow-submodules https://github.com/jpcima/ysfx.git
cmake -S ysfx -B ysfx/build -DCMAKE_BUILD_TYPE=Release -DYSFX_GFX=OFF -DYSFX_PLUGIN=OFF >/dev/null
cmake --build ysfx/build -j4 >/dev/null
g++ -O2 -std=c++17 -Iysfx/include jsfxrun.cpp ysfx/build/libysfx.a $(find ysfx/build -name '*.a' ! -name libysfx.a) -lpthread -ldl -o jsfxrun
echo built ./jsfxrun
