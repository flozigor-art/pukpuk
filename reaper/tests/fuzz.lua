-- Randomized session fuzzer. The simulated user never creates overlapping items,
-- so ANY overlap observed is created by the script. Also checks the script never
-- crashes and nothing goes below 0:00.
-- Usage: python3 run_lua.py fuzz.lua ../AST_Auto_Chapter_Follow.lua [seeds] [ops]

local here = (debug.getinfo(1, "S").source:sub(2):match("(.*/)")) or "./"
package.path = here .. "?.lua;" .. package.path
local H = require("harness")
local script = arg[1] or (here .. "../AST_Auto_Chapter_Follow.lua")
local SEEDS = tonumber(arg[2] or "60")
local OPS = tonumber(arg[3] or "60")

local function rnd(a, b) return a + (b - a) * math.random() end

local function sorted_items(S)
    local arr = {}
    for i = 1, #S.items do arr[i] = S.items[i] end
    table.sort(arr, function(a, b) return a.pos < b.pos end)
    return arr
end

local function overlaps_any(S, s, e, skip)
    for _, it in ipairs(S.items) do
        if not (skip and skip[it]) then
            if math.min(e, it.pos + it.len) - math.max(s, it.pos) > 1e-6 then return true end
        end
    end
    return false
end

local function packs(S)
    local arr = sorted_items(S)
    local out, cur = {}, nil
    for _, it in ipairs(arr) do
        if cur and it.pos - cur.e <= 10 then
            cur.items[#cur.items + 1] = it
            cur.e = math.max(cur.e, it.pos + it.len)
        else
            cur = { s = it.pos, e = it.pos + it.len, items = { it } }
            out[#out + 1] = cur
        end
    end
    return out
end

local function free_gaps(S, min_len)
    local arr = sorted_items(S)
    local gaps = {}
    local prev_e = 0
    for _, it in ipairs(arr) do
        if it.pos - prev_e >= min_len then gaps[#gaps + 1] = { s = prev_e, e = it.pos } end
        prev_e = math.max(prev_e, it.pos + it.len)
    end
    return gaps
end

local function project_end(S)
    local e = 0
    for _, it in ipairs(S.items) do e = math.max(e, it.pos + it.len) end
    return e
end

local ops = {}

ops.record_end = function(ctx)
    local S = ctx.S
    local start = math.max(project_end(S), S.cursor) + rnd(0.5, 14)
    S.cursor = start
    ctx.M.start_record(start)
    local len = rnd(5, 150)
    if math.random() < 0.15 then
        ctx.M.run(len * 0.5)
        -- narrator hits the *** hotkey while recording
        ctx.M.add_marker(S.play_pos, "***")
        ctx.M.run(len * 0.5)
    else
        ctx.M.run(len)
    end
    ctx.M.stop_record()
end

ops.star_cursor = function(ctx)
    local S = ctx.S
    local p = math.max(project_end(S), S.cursor) + rnd(0.1, 3)
    ctx.M.add_marker(p, "***")
end

ops.star_random = function(ctx)
    local S = ctx.S
    local arr = sorted_items(S)
    if #arr == 0 then return ops.star_cursor(ctx) end
    local it = arr[math.random(#arr)]
    local r = math.random()
    local p
    if r < 0.4 then p = it.pos + rnd(0, 14)          -- inside, near start
    elseif r < 0.6 then p = it.pos + rnd(0, it.len)   -- anywhere inside
    else p = it.pos + it.len + rnd(0.05, 3) end        -- right after it
    ctx.M.add_marker(p, "***")
end

ops.delete_star = function(ctx)
    local st = H.stars(ctx)
    if #st == 0 then return end
    ctx.M.delete_marker(st[math.random(#st)].id, false)
end

ops.move_star = function(ctx)
    local st = H.stars(ctx)
    if #st == 0 then return end
    local s = st[math.random(#st)]
    ctx.M.move_marker(s.id, false, math.max(0, s.pos + rnd(-120, 120)))
end

ops.move_pack = function(ctx)
    local S = ctx.S
    local ps = packs(S)
    if #ps == 0 then return end
    local p = ps[math.random(#ps)]
    local skip = {}
    for _, it in ipairs(p.items) do skip[it] = true end
    for _ = 1, 10 do
        local delta = rnd(-400, 400)
        if p.s + delta >= 0 then
            local ok = true
            for _, it in ipairs(p.items) do
                if overlaps_any(S, it.pos + delta, it.pos + it.len + delta, skip) then ok = false break end
            end
            if ok then
                ctx.M.move_items(p.items, delta)
                return
            end
        end
    end
end

ops.move_item = function(ctx)
    local S = ctx.S
    if #S.items == 0 then return end
    local it = S.items[math.random(#S.items)]
    for _ = 1, 10 do
        local delta = rnd(-200, 200)
        if it.pos + delta >= 0 and not overlaps_any(S, it.pos + delta, it.pos + it.len + delta, { [it] = true }) then
            ctx.M.move_items({ it }, delta)
            return
        end
    end
end

ops.delete_item = function(ctx)
    local S = ctx.S
    if #S.items == 0 then return end
    ctx.M.delete_item(S.items[math.random(#S.items)])
end

ops.undo = function(ctx)
    ctx.M.user_undo()
    if math.random() < 0.3 then ctx.M.user_undo() end
end

ops.redo = function(ctx)
    ctx.R.Undo_DoRedo2(ctx.S.proj)
end

ops.punch_in = function(ctx)
    local S = ctx.S
    local gaps = free_gaps(S, 40)
    if #gaps == 0 then return end
    local g = gaps[math.random(#gaps)]
    local start = g.s + rnd(2, 10)
    local len = math.min(rnd(5, 60), (g.e - 2) - start)
    if len < 3 then return end
    S.cursor = start
    ctx.M.start_record(start)
    ctx.M.run(len)
    ctx.M.stop_record()
end

ops.region_around_pack = function(ctx)
    local ps = packs(ctx.S)
    if #ps == 0 then return end
    local p = ps[math.random(#ps)]
    ctx.M.add_marker(math.max(0, p.s - rnd(0, 3)), "user", true, p.e + rnd(0, 3))
end

ops.region_random = function(ctx)
    local s = rnd(0, project_end(ctx.S) + 100)
    ctx.M.add_marker(s, "user", true, s + rnd(5, 200))
end

ops.delete_region = function(ctx)
    local rs = H.regions(ctx)
    if #rs == 0 then return end
    ctx.M.delete_marker(rs[math.random(#rs)].id, true)
end

ops.restart = function(ctx)
    H.restart(ctx)
end

local weights = {
    { "record_end", 30 }, { "star_cursor", 10 }, { "star_random", 5 },
    { "delete_star", 3 }, { "move_star", 2 }, { "move_pack", 5 }, { "move_item", 4 },
    { "delete_item", 3 }, { "undo", 5 }, { "redo", 1 }, { "punch_in", 4 },
    { "region_around_pack", 2 }, { "region_random", 2 }, { "delete_region", 1 },
    { "restart", 1 },
}
local total_w = 0
for _, w in ipairs(weights) do total_w = total_w + w[2] end
local function pick()
    local r = math.random() * total_w
    for _, w in ipairs(weights) do
        r = r - w[2]
        if r <= 0 then return w[1] end
    end
    return weights[1][1]
end

local bad = 0
local stats = { regions = 0, finalizes = 0, msgs = 0 }
for seed = 1, SEEDS do
    math.randomseed(seed)
    local ctx = H.start(script)
    if math.random() < 0.5 then ctx.S.cursor = rnd(0, 30) else ctx.S.cursor = rnd(3000, 5000) end
    -- random dialog answers
    for _ = 1, 200 do
        local r = math.random()
        ctx.M.answer(r < 0.6 and nil or (r < 0.8 and 2 or 7))
    end
    local history = {}
    local failed = nil
    for step = 1, OPS do
        local name = pick()
        history[#history + 1] = name
        ops[name](ctx)
        ctx.M.run(rnd(0.5, 4))
        local problems = H.check(ctx, { skip_owner_check = true })
        local real = {}
        for _, p in ipairs(problems) do
            if not p:find("REGIONS OVERLAP", 1, true) then real[#real + 1] = p end
        end
        if #real > 0 then
            failed = { step = step, problems = real }
            break
        end
    end
    ctx.M.run(5)
    stats.regions = stats.regions + #H.regions(ctx)
    stats.msgs = stats.msgs + #ctx.S.msgbox_log
    if failed then
        bad = bad + 1
        print(string.format("SEED %d FAILED at step %d (%s)", seed, failed.step, history[#history]))
        for _, p in ipairs(failed.problems) do print("   " .. p) end
        print("   ops: " .. table.concat(history, ","))
        if os.getenv("DUMP") then H.dump(ctx, "seed " .. seed) end
    end
end
print(string.format("%d/%d seeds clean (regions at end: %d, dialogs: %d)",
    SEEDS - bad, SEEDS, stats.regions, stats.msgs))
if bad > 0 then os.exit(1) end
