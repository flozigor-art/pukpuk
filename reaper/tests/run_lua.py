#!/usr/bin/env python3
"""Run a Lua 5.4 file through lupa (pip install lupa) when no lua binary exists.

Usage: python3 run_lua.py file.lua [args...]   (arg[0] = file.lua, arg[1..] = args)
"""
import sys
from lupa import lua54

lua = lua54.LuaRuntime(unpack_returned_tuples=True)
lua.execute("arg = {}")
args = lua.eval("arg")
for i, a in enumerate(sys.argv[1:]):
    args[i] = a
ok = lua.eval("function(path) local ok, err = xpcall(dofile, debug.traceback, path); if not ok then io.stderr:write(tostring(err), '\\n') end; return ok end")(sys.argv[1])
sys.exit(0 if ok else 1)
