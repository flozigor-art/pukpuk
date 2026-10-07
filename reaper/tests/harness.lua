-- Scenario harness: loads the chapter script against the REAPER mock and
-- provides "user" helpers plus invariant checks.

local here = (debug.getinfo(1, "S").source:sub(2):match("(.*/)")) or "./"
package.path = here .. "?.lua;" .. package.path
local Mock = require("reaper_mock")

local H = {}

local function fmt_t(t)
    local neg = t < 0
    local ms = math.floor(math.abs(t) * 1000 + 0.5)
    local h = ms // 3600000
    local m = (ms // 60000) % 60
    local s = (ms % 60000) / 1000
    return string.format("%s%d:%02d:%06.3f", neg and "-" or "", h, m, s)
end
H.fmt_t = fmt_t

local function load_script(script_path, R)
    local env = setmetatable({ reaper = R }, { __index = _G })
    local chunk, err = loadfile(script_path, "t", env)
    if not chunk then error(err) end
    chunk()
end

-- opts.setup(M) runs before the script starts (pre-existing project state).
function H.start(script_path, opts)
    opts = opts or {}
    local M = Mock
    local R = M.new(opts)
    if opts.setup then opts.setup(M, M.S) end
    load_script(script_path, R)
    local ctx = { M = M, R = R, S = M.S, log = {}, script = script_path }
    return ctx
end

-- Start a second instance on the same project (toolbar re-launch / REAPER restart
-- with the project already open). The first instance stops on its next tick.
function H.restart(ctx)
    ctx.S.deferred = nil
    load_script(ctx.script, ctx.R)
end

-- user helpers --------------------------------------------------------

function H.record(ctx, len, gap)
    local M = ctx.M
    local pos = ctx.S.cursor + (gap or 1.0)
    M.start_record(pos)
    M.run(len)
    local item = M.stop_record()
    M.run(1.0)
    return item
end

function H.star(ctx, pos)
    local M = ctx.M
    local id = M.add_marker(pos or (ctx.S.cursor + 0.5), "***")
    M.run(1.0)
    return id
end

function H.idle(ctx, sec) ctx.M.run(sec or 2.0) end

-- inspection -----------------------------------------------------------

function H.dump(ctx, title)
    local S = ctx.S
    print("---- " .. (title or "state") .. " ----")
    local items = {}
    for i = 1, #S.items do items[i] = S.items[i] end
    table.sort(items, function(a, b) return a.pos < b.pos end)
    for _, r in ipairs(items) do
        print(string.format("  item %s  %s .. %s  home=%s", r.guid:sub(-4), fmt_t(r.pos), fmt_t(r.pos + r.len),
            tostring(r.ext["AST_CHAPTER_REGION"])))
    end
    for _, m in ipairs(ctx.M.marks()) do
        if m.isrgn then
            print(string.format("  REGION #%d '%s'  %s .. %s  color=%s", m.id, m.name, fmt_t(m.pos), fmt_t(m.rgnend), tostring(m.color)))
        else
            print(string.format("  marker #%d '%s'  %s", m.id, m.name, fmt_t(m.pos)))
        end
    end
    for _, b in ipairs(S.msgbox_log) do
        print(string.format("  [msgbox t=%.1f ans=%s] %s", b.t, tostring(b.ans), (b.msg:gsub("\n", " "))))
    end
end

-- invariants ------------------------------------------------------------

function H.check(ctx, opts)
    opts = opts or {}
    local S = ctx.S
    local problems = {}
    local items = {}
    for i = 1, #S.items do items[i] = S.items[i] end
    table.sort(items, function(a, b) return a.pos < b.pos end)
    for i = 1, #items do
        local a = items[i]
        if a.pos < -1e-6 then problems[#problems + 1] = "item before 0: " .. a.guid end
        for j = i + 1, #items do
            local b = items[j]
            if b.pos >= a.pos + a.len - 1e-6 then break end
            problems[#problems + 1] = string.format("OVERLAP %s [%s..%s] x %s [%s..%s]",
                a.guid:sub(-4), fmt_t(a.pos), fmt_t(a.pos + a.len), b.guid:sub(-4), fmt_t(b.pos), fmt_t(b.pos + b.len))
        end
    end
    local regions = {}
    for _, m in ipairs(ctx.M.marks()) do if m.isrgn then regions[#regions + 1] = m end end
    for i = 1, #regions do
        for j = i + 1, #regions do
            local a, b = regions[i], regions[j]
            if math.min(a.rgnend, b.rgnend) - math.max(a.pos, b.pos) > 1e-6 then
                problems[#problems + 1] = string.format("REGIONS OVERLAP #%d [%s..%s] x #%d [%s..%s]",
                    a.id, fmt_t(a.pos), fmt_t(a.rgnend), b.id, fmt_t(b.pos), fmt_t(b.rgnend))
            end
        end
    end
    -- every owned item lies inside its region
    if not opts.skip_owner_check then
        for _, r in ipairs(items) do
            local h = tonumber(r.ext["AST_CHAPTER_REGION"])
            if h then
                local reg
                for _, m in ipairs(regions) do if m.id == h then reg = m end end
                if reg and (r.pos < reg.pos - 1e-6 or r.pos + r.len > reg.rgnend + 1e-6) then
                    if opts.strict_owner then
                        problems[#problems + 1] = string.format("item %s owned by #%d but outside it", r.guid:sub(-4), h)
                    end
                end
            end
        end
    end
    if not ctx.M.alive() then problems[#problems + 1] = "SCRIPT STOPPED (crash?)" end
    return problems
end

function H.regions(ctx)
    local out = {}
    for _, m in ipairs(ctx.M.marks()) do if m.isrgn then out[#out + 1] = m end end
    return out
end

function H.stars(ctx)
    local out = {}
    for _, m in ipairs(ctx.M.marks()) do if not m.isrgn and m.name == "***" then out[#out + 1] = m end end
    return out
end

function H.owned_by(ctx, rid)
    local out = {}
    for _, r in ipairs(ctx.S.items) do
        if tonumber(r.ext["AST_CHAPTER_REGION"]) == rid then out[#out + 1] = r end
    end
    table.sort(out, function(a, b) return a.pos < b.pos end)
    return out
end

return H
