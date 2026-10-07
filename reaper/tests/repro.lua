local here = (debug.getinfo(1, "S").source:sub(2):match("(.*/)")) or "./"
package.path = here .. "?.lua;" .. package.path
local H = require("harness")
local script = arg[1] or (here .. "../AST_Auto_Chapter_Follow.lua")

local function takes(ctx, n, len)
    for i = 1, n do H.record(ctx, len, 1.0) end
end

print("=== S1: empty project, first chapter recorded without a leading *** ===")
do
    local ctx = H.start(script)
    takes(ctx, 4, 60)         -- chapter 1 (no star)
    H.star(ctx)               -- *** before chapter 2
    takes(ctx, 4, 60)         -- chapter 2
    H.star(ctx)               -- *** before chapter 3
    takes(ctx, 3, 30)         -- chapter 3 -> matures, chapter 2 finalized
    H.idle(ctx, 3)
    H.dump(ctx, "S1 result")
    for _, p in ipairs(H.check(ctx)) do print("  !! " .. p) end
end

print("\n=== S2: empty project, *** at 0:00, accept +1h ===")
do
    local ctx = H.start(script)
    H.star(ctx, 0)
    ctx.S.cursor = 0
    takes(ctx, 4, 60)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    H.dump(ctx, "S2 result")
    for _, p in ipairs(H.check(ctx)) do print("  !! " .. p) end
end

print("\n=== S3: *** at 0:00, decline +1h, user deletes first *** ===")
do
    local ctx = H.start(script)
    local a = H.star(ctx, 0)
    ctx.S.cursor = 0
    takes(ctx, 4, 60)
    H.star(ctx)
    ctx.M.answer(2) -- Cancel the +1h offer
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    ctx.M.delete_marker(a, false)
    H.idle(ctx, 2)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    H.dump(ctx, "S3 result")
    for _, p in ipairs(H.check(ctx)) do print("  !! " .. p) end
end
