-- Edge-case scenarios with assertions.
-- Usage: python3 run_lua.py scenarios.lua ../AST_Auto_Chapter_Follow.lua [filter]

local here = (debug.getinfo(1, "S").source:sub(2):match("(.*/)")) or "./"
package.path = here .. "?.lua;" .. package.path
local H = require("harness")
local script = arg[1] or (here .. "../AST_Auto_Chapter_Follow.lua")
local filter = arg[2]

local tests = {}
local function test(name, fn, opts) tests[#tests + 1] = { name = name, fn = fn, opts = opts } end

local failures = {}
local function expect(cond, msg)
    if not cond then error("EXPECT FAILED: " .. msg, 2) end
end

local function takes(ctx, n, len, gap)
    local out = {}
    for i = 1, n do out[#out + 1] = H.record(ctx, len, gap or 1.0) end
    return out
end

local function region_of(ctx, item)
    return tonumber(item.ext["AST_CHAPTER_REGION"])
end

local function region_by_id(ctx, rid)
    for _, r in ipairs(H.regions(ctx)) do if r.id == rid then return r end end
end

local function all_owned_by(ctx, items, rid)
    for _, it in ipairs(items) do
        if region_of(ctx, it) ~= rid then return false end
    end
    return true
end

local function contiguous_after(items)
    -- returns true if items keep gaps <= 10 s (one pack)
    table.sort(items, function(a, b) return a.pos < b.pos end)
    for i = 2, #items do
        if items[i].pos - (items[i - 1].pos + items[i - 1].len) > 10 + 1e-6 then return false end
    end
    return true
end

local function span(items)
    local s, e = math.huge, -math.huge
    for _, it in ipairs(items) do
        s = math.min(s, it.pos); e = math.max(e, it.pos + it.len)
    end
    return s, e
end

local function msgcount(ctx, pattern)
    local n = 0
    for _, b in ipairs(ctx.S.msgbox_log) do if b.msg:find(pattern, 1, true) then n = n + 1 end end
    return n
end

local function room(ctx, t) ctx.S.cursor = t or 4000 end

------------------------------------------------------------------------

test("first chapter without leading *** is pushed aside, never overlapped", function(ctx)
    local ch1 = takes(ctx, 4, 60)
    H.star(ctx)
    local ch2 = takes(ctx, 4, 60)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    local regs = H.regions(ctx)
    expect(#regs == 1, "one region expected, got " .. #regs)
    expect(all_owned_by(ctx, ch2, regs[1].id), "chapter 2 owned by the region")
    for _, it in ipairs(ch1) do expect(region_of(ctx, it) == nil, "chapter 1 stays unowned") end
    local _, e1 = span(ch1)
    local s2 = span(ch2)
    expect(s2 - e1 >= 63 - 1e-6, "60 s layout gap between packs (padded), got " .. (s2 - e1))
end)

test("five chapters from an empty project with *** at 0:00", function(ctx)
    H.star(ctx, 0); ctx.S.cursor = 0
    local chapters = { takes(ctx, 4, 60) }
    for k = 2, 5 do
        H.star(ctx)
        chapters[k] = takes(ctx, 4, 60)
    end
    H.idle(ctx, 3)
    local regs = H.regions(ctx)
    expect(#regs == 4, "4 finalized chapters expected, got " .. #regs)
    for k = 1, 4 do
        local rid = region_of(ctx, chapters[k][1])
        expect(rid, "chapter " .. k .. " owned")
        expect(all_owned_by(ctx, chapters[k], rid), "chapter " .. k .. " fully owned by one region")
        local r = region_by_id(ctx, rid)
        local s, e = span(chapters[k])
        expect(math.abs(r.pos - (s - 1.5)) < 1e-3 and math.abs(r.rgnend - (e + 1.5)) < 1e-3, "region pad 1.5 s")
    end
    table.sort(regs, function(a, b) return a.pos < b.pos end)
    for i = 2, #regs do
        expect(regs[i].pos - regs[i - 1].rgnend >= 60 - 1e-3, "regions 60 s apart")
    end
    local s5 = span(chapters[5])
    expect(s5 - 1.5 - regs[#regs].rgnend >= 60 - 1e-3, "current chapter 60 s after last region")
    expect(#H.stars(ctx) == 1, "only the current chapter *** remains")
    expect(msgcount(ctx, "1:00:00") == 1, "exactly one +1h offer")
end)

test("declined +1h is offered again after the next recording", function(ctx)
    H.star(ctx, 0); ctx.S.cursor = 0
    takes(ctx, 4, 60)
    H.star(ctx)
    ctx.M.answer(2) -- Cancel
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 0, "nothing finalized after Cancel")
    expect(msgcount(ctx, "1:00:00") == 1, "one offer so far")
    takes(ctx, 1, 20)
    H.idle(ctx, 3)
    expect(msgcount(ctx, "1:00:00") == 2, "offered again after the next take")
    expect(#H.regions(ctx) == 1, "finalized after OK")
end)

test("A+B+C override finalizes during recording without touching the take", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 3, 60)
    H.star(ctx)
    takes(ctx, 1, 20)               -- chapter 2 not mature
    H.star(ctx)                     -- C
    ctx.M.start_record(ctx.S.cursor + 1)
    ctx.M.run(5)
    expect(#H.regions(ctx) == 1, "A->B finalized during recording")
    expect(region_of(ctx, ch1[1]) ~= nil, "chapter 1 owned")
    ctx.M.run(30)
    ctx.M.stop_record()
    H.idle(ctx, 3)
end)

test("split command: left part packs left, right part becomes 'rename', one undo restores", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 6, 30)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "chapter 1 finalized")
    local rid = region_of(ctx, ch1[1])
    local before = {}
    for _, it in ipairs(ch1) do before[it.guid] = it.pos end
    local star_count = #H.stars(ctx)

    -- *** in the 1 s gap between take 3 and take 4
    H.star(ctx, ch1[3].pos + ch1[3].len + 0.5)
    H.idle(ctx, 2)
    local regs = H.regions(ctx)
    expect(#regs == 2, "split produced 2 regions, got " .. #regs)
    expect(#H.stars(ctx) == star_count, "split *** consumed, others untouched")
    local left = { ch1[1], ch1[2], ch1[3] }
    local right = { ch1[4], ch1[5], ch1[6] }
    expect(all_owned_by(ctx, left, rid), "left stays in old region")
    local new_rid = region_of(ctx, ch1[4])
    expect(new_rid and new_rid ~= rid and all_owned_by(ctx, right, new_rid), "right in new region")
    expect(region_by_id(ctx, new_rid).name == "rename", "new region named rename")
    for _, it in ipairs(right) do expect(math.abs(it.pos - before[it.guid]) < 1e-6, "right part did not move") end
    local r1, r2 = region_by_id(ctx, rid), region_by_id(ctx, new_rid)
    expect(r2.pos - r1.rgnend >= 60 - 1e-3, "60 s between the halves")

    ctx.M.user_undo()
    H.idle(ctx, 3)
    local items = ctx.S.items
    for _, it in ipairs(items) do
        if before[it.guid] then
            expect(math.abs(it.pos - before[it.guid]) < 1e-6, "undo restored positions")
        end
    end
    expect(#H.regions(ctx) == 1, "undo restored one region")
    expect(#H.stars(ctx) == star_count, "undo removed the split ***")
end)

test("split pushes region-less material on the left instead of overlapping it", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 6, 30)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    -- user drops a loose item 20 s before chapter 1
    local loose = ctx.M.add_item(ch1[1].pos - 1.5 - 20 - 30, 30)
    H.idle(ctx, 2)
    H.star(ctx, ch1[3].pos + ch1[3].len + 0.5)
    H.idle(ctx, 2)
    expect(#H.regions(ctx) == 2, "split done")
    expect(loose.pos + loose.len <= ch1[1].pos - 1e-6, "loose item still left of chapter")
end)

test("undo of finalize is not fought; A keeps its role", function(ctx)
    room(ctx)
    local a = H.star(ctx)
    local ch1 = takes(ctx, 3, 60)
    H.star(ctx)
    local ch2 = takes(ctx, 3, 30)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "finalized")
    ctx.M.user_undo()
    H.idle(ctx, 5)
    expect(#H.regions(ctx) == 0, "script did not redo the finalize after undo")
    expect(#H.stars(ctx) == 2, "A is back")
    takes(ctx, 1, 20)               -- next user action ends the pause
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "finalized again after the next change")
    local rid = region_of(ctx, ch1[1])
    expect(rid and all_owned_by(ctx, ch1, rid), "same chapter 1 material finalized (A kept its role)")
    for _, it in ipairs(ch2) do expect(region_of(ctx, it) == nil, "chapter 2 still pending") end
end)

local function two_chapters(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 3, 60)
    H.star(ctx)
    local ch2 = takes(ctx, 3, 60)
    H.star(ctx)
    local ch3 = takes(ctx, 3, 60)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 2, "two chapters finalized, got " .. #H.regions(ctx))
    return ch1, ch2, ch3
end

test("collision by user move: No undoes the move", function(ctx)
    local ch1, ch2 = two_chapters(ctx)
    local _, e1 = span(ch1)
    local s2 = span(ch2)
    ctx.M.answer(7) -- No
    ctx.M.move_items(ch2, -(s2 - e1) + 8)
    H.idle(ctx, 3)
    expect(msgcount(ctx, "Объединить главы") == 1, "merge dialog shown")
    local s2b = span(ch2)
    expect(math.abs(s2b - s2) < 1e-6, "move undone")
    expect(#H.regions(ctx) == 2, "both regions kept")
end)

test("collision by user move: Yes merges", function(ctx)
    local ch1, ch2 = two_chapters(ctx)
    local _, e1 = span(ch1)
    local s2 = span(ch2)
    ctx.M.answer(6) -- Yes
    ctx.M.move_items(ch2, -(s2 - e1) + 8)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "merged into one region")
    local rid = region_of(ctx, ch1[1])
    expect(all_owned_by(ctx, ch2, rid), "chapter 2 joined chapter 1")
end)

test("bridging take recorded between chapters: asks after recording, No keeps regions apart", function(ctx)
    local ch1, ch2 = two_chapters(ctx)
    local _, e1 = span(ch1)
    local s2 = span(ch2)
    ctx.S.cursor = e1 + 5 - 1.0
    ctx.M.answer(7) -- leave as is
    ctx.M.start_record(e1 + 5)
    ctx.M.run((s2 - 6) - (e1 + 5))
    expect(msgcount(ctx, "Объединить главы") == 0, "no dialog during recording")
    ctx.M.stop_record()
    H.idle(ctx, 3)
    expect(msgcount(ctx, "Объединить главы") == 1, "dialog after recording")
    expect(#H.regions(ctx) == 2, "regions kept")
end)

test("orphan region in silence is deleted, but not within the same cycle", function(ctx)
    room(ctx)
    takes(ctx, 2, 30)
    H.idle(ctx, 2)
    local rid = ctx.M.add_marker(100, "user region", true, 120)
    ctx.M.run(0.6)
    expect(#H.regions(ctx) == 1, "still present after first observation")
    H.idle(ctx, 2)
    expect(#H.regions(ctx) == 0, "deleted after second cycle")
end)

test("recording before the chapter: reflow waits, recorded take never overlapped", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 2, 60)
    H.star(ctx)
    takes(ctx, 1, 20)
    local end_ch2 = ctx.S.cursor
    -- punch-in far left of chapter 1, then insert C while recording
    ctx.S.cursor = ch1[1].pos - 100
    ctx.M.start_record(ch1[1].pos - 100)
    ctx.M.run(2)
    ctx.M.add_marker(end_ch2 + 0.5, "***")
    ctx.M.run(40)
    expect(#H.regions(ctx) == 0, "no reflow toward the running recording")
    ctx.M.stop_record()
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "finalized after recording stopped")
end)

test("+1h shift keeps markers aligned even if REAPER does not move them", function(ctx)
    H.star(ctx, 0); ctx.S.cursor = 0
    local ch1 = takes(ctx, 4, 60)
    H.star(ctx)
    local ch2 = takes(ctx, 3, 30)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "finalized")
    local st = H.stars(ctx)
    expect(#st == 1 and math.abs(st[1].pos - (ch2[1].pos - 0.5)) < 1e-3, "*** still right before chapter 2")
end, { lock_markers_on_insert = true })

test("empty A->B: warned after recording and A removed", function(ctx)
    room(ctx)
    H.star(ctx)
    H.star(ctx, ctx.S.cursor + 10)
    ctx.S.cursor = ctx.S.cursor + 10
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    expect(msgcount(ctx, "нет айтемов") == 1, "warning shown once")
    expect(#H.stars(ctx) == 1, "A removed")
    expect(#H.regions(ctx) == 0, "no region created")
end)

test("fourth *** is deleted as the newest insertion", function(ctx)
    for _ = 1, 6 do ctx.M.answer(2) end -- keep declining the +1h offer
    H.star(ctx, 0); ctx.S.cursor = 0
    takes(ctx, 3, 30)
    H.star(ctx)
    takes(ctx, 1, 20)
    H.star(ctx)
    takes(ctx, 1, 20)
    local before = H.stars(ctx)
    expect(#before == 3, "three pending stars")
    H.star(ctx, ctx.S.cursor + 50)
    H.idle(ctx, 2)
    local after = H.stars(ctx)
    expect(#after == 3, "4th deleted")
    for i = 1, 3 do expect(after[i].id == before[i].id, "the three older stars kept") end
end)

test("duplicate *** within 5 s: the kept one inherits the older rank", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 3, 30)
    local b = H.star(ctx, ctx.S.cursor + 6)
    ctx.S.cursor = ctx.S.cursor + 6
    takes(ctx, 1, 20)
    local bpos
    for _, st in ipairs(H.stars(ctx)) do if st.id == b then bpos = st.pos end end
    -- newer marker 2 s before B: B (timeline-later) is deleted, the new one takes its role
    H.star(ctx, bpos - 2)
    H.idle(ctx, 2)
    expect(#H.stars(ctx) == 2, "duplicate removed")
    takes(ctx, 2, 30)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 1, "chapter 1 finalized with the inherited role")
    local rid = region_of(ctx, ch1[1])
    expect(rid and all_owned_by(ctx, ch1, rid), "region holds exactly chapter 1")
end)

test("restart with existing chapters changes nothing", function(ctx)
    local ch1, ch2, ch3 = two_chapters(ctx)
    local before = {}
    for _, it in ipairs(ctx.S.items) do before[it.guid] = it.pos end
    local regs_before = #H.regions(ctx)
    H.restart(ctx)
    H.idle(ctx, 5)
    for _, it in ipairs(ctx.S.items) do
        expect(math.abs(it.pos - before[it.guid]) < 1e-6, "no item moved on restart")
    end
    expect(#H.regions(ctx) == regs_before, "regions kept")
end)

test("40 min helper appears live during a long take", function(ctx)
    room(ctx)
    H.star(ctx)
    ctx.M.start_record(ctx.S.cursor + 1)
    local start = ctx.S.cursor
    ctx.M.run(40 * 60 + 5)
    local found = false
    for _, m in ipairs(ctx.M.marks()) do
        if m.name == "40 min" and math.abs(m.pos - (start + 2400)) < 1e-3 then found = true end
    end
    expect(found, "40 min marker during recording")
    ctx.M.run(5)
    ctx.M.stop_record()
    H.idle(ctx, 2)
    local n = 0
    for _, m in ipairs(ctx.M.marks()) do if m.name == "40 min" then n = n + 1 end end
    expect(n == 1, "exactly one 40 min marker after stop")
end)

test("deep *** inside a take blocks with one message", function(ctx)
    room(ctx)
    H.star(ctx)
    takes(ctx, 3, 30)
    ctx.M.start_record(ctx.S.cursor + 1)
    ctx.M.run(30)
    ctx.M.add_marker(ctx.S.play_pos, "***")
    ctx.M.run(30)
    ctx.M.stop_record()
    H.idle(ctx, 5)
    expect(msgcount(ctx, "остановлено") == 1, "blocker shown once")
    expect(#H.regions(ctx) == 0, "nothing finalized")
end)

test("third *** before the second blocks", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 2, 30)
    H.star(ctx)
    takes(ctx, 1, 20)
    H.star(ctx, ch1[1].pos + 40) -- C between A and B (in the 1 s gap)
    H.idle(ctx, 3)
    expect(msgcount(ctx, "третий") == 1, "ORDER3 blocker")
    expect(#H.regions(ctx) == 0, "nothing finalized")
end)

test("long chapter turns red and back", function(ctx)
    room(ctx)
    H.star(ctx)
    local ch1 = takes(ctx, 61, 60)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    local r = H.regions(ctx)[1]
    expect(r and r.color ~= 0, "region red")
    ctx.M.delete_item(ch1[61]); H.idle(ctx, 1)
    ctx.M.delete_item(ch1[60]); H.idle(ctx, 1)
    ctx.M.delete_item(ch1[59]); H.idle(ctx, 2)
    r = H.regions(ctx)[1]
    expect(r.color == 0, "back to default color")
end)

test("chapter near 0:00 does not freeze automation", function(ctx)
    ctx.M.answer(2) -- decline the +1h offer
    local it = ctx.M.add_item(0.5, 30)
    ctx.M.add_marker(0, "chapter", true, 32)
    H.idle(ctx, 3)
    local r = H.regions(ctx)[1]
    expect(r and r.pos == 0, "region clamped at 0")
    expect(region_of(ctx, it) == r.id, "adopted")
    -- automation still works afterwards
    ctx.S.cursor = 100
    H.star(ctx)
    local ch = takes(ctx, 2, 30)
    H.star(ctx)
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    expect(#H.regions(ctx) == 2, "next chapter finalized")
end)

test("detached piece moved into the current chapter area is transferred on finalize", function(ctx)
    local ch1, ch2, ch3 = two_chapters(ctx)
    -- move the last take of chapter 1 into the free space right after chapter 3
    ctx.S.cursor = ctx.S.cursor + 0
    local last = ch1[#ch1]
    local _, e3 = span(ch3)
    ctx.M.move_items({ last }, (e3 + 3) - last.pos)
    H.idle(ctx, 3)
    H.star(ctx, ctx.S.cursor + 200)
    ctx.S.cursor = ctx.S.cursor + 200
    takes(ctx, 3, 30)
    H.idle(ctx, 3)
    local r1 = region_of(ctx, ch1[1])
    expect(r1 and region_by_id(ctx, r1), "chapter 1 region survives")
    expect(region_of(ctx, ch3[1]) and region_of(ctx, ch3[1]) ~= r1, "chapter 3 got its own region")
end)

------------------------------------------------------------------------

local passed = 0
for _, t in ipairs(tests) do
    if not filter or t.name:find(filter, 1, true) then
        local ctx = H.start(script, t.opts)
        local ok, err = xpcall(t.fn, debug.traceback, ctx)
        local problems = H.check(ctx)
        if ok and #problems == 0 then
            passed = passed + 1
            print("PASS  " .. t.name)
        else
            print("FAIL  " .. t.name)
            if not ok then print("      " .. tostring(err):gsub("\n", "\n      ")) end
            for _, p in ipairs(problems) do print("      !! " .. p) end
            if os.getenv("DUMP") then H.dump(ctx, t.name) end
            failures[#failures + 1] = t.name
        end
    end
end
print(string.format("\n%d passed, %d failed", passed, #failures))
if #failures > 0 then os.exit(1) end
