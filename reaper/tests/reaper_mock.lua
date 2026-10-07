-- Minimal, deterministic mock of the REAPER API surface used by
-- AST_Auto_Chapter_Follow.lua. It is a test double, not a full emulator:
--   * simulated clock (time_precise) driven by the harness;
--   * one project tab;
--   * items: position/length/GUID/P_EXT; pointers are recreated on undo, so a
--     script that keeps stale MediaItem* across Undo fails loudly;
--   * markers/regions with separate ID namespaces, enumerated by position;
--     new IDs take the lowest free number (stresses ID reuse);
--   * full-state undo history with descriptions;
--   * recording: items appear only when recording stops (REAPER behaviour);
--   * action 40200 inserts empty space at the time selection.

local M = {}

local function deepcopy(v)
    if type(v) ~= "table" then return v end
    local out = {}
    for k, x in pairs(v) do out[k] = deepcopy(x) end
    return out
end

function M.new(opts)
    opts = opts or {}
    local R = {}            -- the `reaper` table given to the script
    local S = {             -- simulation state
        now = 1000.0,
        items = {},         -- array of item records {ptr, guid, pos, len, ext}
        records = {},       -- guid -> record (stable across undo)
        marks = {},         -- array of {isrgn, pos, rgnend, name, id, color}
        state_count = 1,
        ext = {},
        undo = {},
        undo_pos = 0,
        rec = nil,          -- {start, track}
        play_pos = 0,
        cursor = 0,
        ts = {0, 0},
        loop = {0, 0},
        msgbox_log = {},
        msgbox_answers = {},-- queue of answers; default per type below
        deferred = nil,
        atexit_fn = nil,
        guid_counter = 0,
        toggle = nil,
        errors = {},
        undo_block_depth = 0,
        lock_markers_on_insert = opts.lock_markers_on_insert or false,
    }
    M.S = S

    local proj = { name = "proj" }
    S.proj = proj

    local function bump() S.state_count = S.state_count + 1 end

    local function new_guid()
        S.guid_counter = S.guid_counter + 1
        return string.format("{00000000-0000-0000-0000-%012d}", S.guid_counter)
    end

    local function make_ptr(rec)
        local p = setmetatable({}, { __tostring = function() return "MediaItem:" .. rec.guid end })
        rec.ptr = p
        return p
    end

    local function find_item(ptr)
        for i = 1, #S.items do
            if S.items[i].ptr == ptr then return S.items[i] end
        end
        error("invalid MediaItem* (stale pointer after undo?)", 3)
    end

    local function sorted_marks()
        local arr = {}
        for i = 1, #S.marks do arr[i] = S.marks[i] end
        table.sort(arr, function(a, b)
            if a.pos ~= b.pos then return a.pos < b.pos end
            if a.isrgn ~= b.isrgn then return not a.isrgn end
            return a.id < b.id
        end)
        return arr
    end

    -- state capture for undo
    local function capture()
        local items = {}
        for i = 1, #S.items do
            local r = S.items[i]
            items[i] = { guid = r.guid, pos = r.pos, len = r.len, ext = deepcopy(r.ext) }
        end
        return { items = items, marks = deepcopy(S.marks) }
    end
    -- Undo keeps the harness-side record tables (looked up by GUID) but gives
    -- every item a NEW MediaItem* pointer, like REAPER may do when it reloads
    -- state. A script that keeps old pointers across Undo fails loudly.
    local function restore(st)
        S.items = {}
        for i = 1, #st.items do
            local src = st.items[i]
            local r = S.records[src.guid] or {}
            r.guid, r.pos, r.len, r.ext = src.guid, src.pos, src.len, deepcopy(src.ext)
            S.records[src.guid] = r
            make_ptr(r)
            S.items[i] = r
        end
        S.marks = deepcopy(st.marks)
        bump()
    end
    function M.add_undo_point(desc)
        for i = #S.undo, S.undo_pos + 1, -1 do S.undo[i] = nil end
        S.undo[#S.undo + 1] = { desc = desc, state = capture() }
        S.undo_pos = #S.undo
    end
    M.add_undo_point("<initial>")

    ------------------------------------------------------------------
    -- harness-side helpers (simulate the user)
    ------------------------------------------------------------------
    function M.add_item(pos, len, opts2)
        local r = { guid = new_guid(), pos = pos, len = len, ext = {} }
        S.records[r.guid] = r
        make_ptr(r)
        S.items[#S.items + 1] = r
        bump()
        if not (opts2 and opts2.no_undo) then M.add_undo_point("Add media item") end
        return r
    end
    function M.add_marker(pos, name, isrgn, rgnend)
        local id = R.AddProjectMarker2(proj, isrgn or false, pos, rgnend or 0, name, -1, 0)
        M.add_undo_point(isrgn and "Insert region" or "Insert marker")
        return id
    end
    function M.delete_marker(id, isrgn)
        R.DeleteProjectMarker(proj, id, isrgn or false)
        M.add_undo_point("Remove marker")
    end
    function M.move_marker(id, isrgn, pos)
        for i = 1, #S.marks do
            local m = S.marks[i]
            if m.id == id and m.isrgn == (isrgn or false) then
                local len = m.rgnend - m.pos
                m.pos = pos
                if m.isrgn then m.rgnend = pos + len end
            end
        end
        bump()
        M.add_undo_point("Move marker")
    end
    function M.move_items(recs, delta)
        for i = 1, #recs do recs[i].pos = recs[i].pos + delta end
        bump()
        M.add_undo_point("Move media items")
    end
    function M.delete_item(rec)
        for i = 1, #S.items do
            if S.items[i] == rec then table.remove(S.items, i); break end
        end
        bump()
        M.add_undo_point("Delete media item")
    end
    function M.start_record(pos)
        S.rec = { start = pos }
        S.play_pos = pos
        S.cursor = pos
    end
    function M.stop_record()
        local r = S.rec
        S.rec = nil
        local len = S.play_pos - r.start
        local item = M.add_item(r.start, len, { no_undo = true })
        M.add_undo_point("Recorded media")
        S.cursor = S.play_pos
        return item
    end
    function M.user_undo() return R.Undo_DoUndo2(proj) end
    function M.items() return S.items end
    function M.marks() return sorted_marks() end
    function M.answer(v) S.msgbox_answers[#S.msgbox_answers + 1] = v end

    -- Advance the simulated clock, running the deferred loop.
    function M.run(seconds, step)
        step = step or 0.05
        local t_end = S.now + seconds
        while S.now < t_end - 1e-9 do
            S.now = S.now + step
            if S.rec then S.play_pos = S.play_pos + step end
            local f = S.deferred
            S.deferred = nil
            if f then
                local ok, err = xpcall(f, debug.traceback)
                if not ok then
                    S.errors[#S.errors + 1] = err
                    print("SCRIPT ERROR: " .. tostring(err))
                    S.deferred = nil
                    return
                end
            end
        end
    end
    function M.alive() return S.deferred ~= nil end

    ------------------------------------------------------------------
    -- reaper.* API
    ------------------------------------------------------------------
    R.time_precise = function() return S.now end
    R.defer = function(f) S.deferred = f end
    R.atexit = function(f) S.atexit_fn = f end
    R.get_action_context = function() return true, "script.lua", 0, 12345, 0, 0, 0 end
    R.SetToggleCommandState = function(_, _, v) S.toggle = v end
    R.RefreshToolbar2 = function() end
    R.GetExtState = function(sec, key) return (S.ext[sec .. "/" .. key]) or "" end
    R.SetExtState = function(sec, key, v) S.ext[sec .. "/" .. key] = v end
    R.DeleteExtState = function(sec, key) S.ext[sec .. "/" .. key] = nil end
    R.APIExists = function(name) return R[name] ~= nil end
    R.ColorToNative = function(r, g, b) return r | (g << 8) | (b << 16) end
    R.EnumProjects = function(idx)
        if idx == -1 or idx == 0 then return proj, "" end
        return nil
    end
    R.GetProjectStateChangeCount = function() return S.state_count end
    R.GetPlayStateEx = function() return S.rec and 5 or 0 end
    R.GetPlayState = function() return S.rec and 5 or 0 end
    R.GetPlayPositionEx = function() return S.play_pos end
    R.GetPlayPosition = function() return S.play_pos end
    R.GetPlayPosition2Ex = function() return S.play_pos end
    R.GetCursorPositionEx = function() return S.cursor end
    R.GetCursorPosition = function() return S.cursor end
    R.SetEditCurPos2 = function(_, p) S.cursor = p end
    R.SetEditCurPos = function(p) S.cursor = p end
    R.PreventUIRefresh = function() end
    R.UpdateTimeline = function() end
    R.UpdateArrange = function() end

    R.ValidatePtr2 = function(_, ptr, typ)
        if typ ~= "MediaItem*" then return false end
        for i = 1, #S.items do if S.items[i].ptr == ptr then return true end end
        return false
    end
    R.ValidatePtr = function(ptr, typ) return R.ValidatePtr2(proj, ptr, typ) end

    R.CountMediaItems = function() return #S.items end
    R.GetMediaItem = function(_, i)
        local r = S.items[i + 1]
        return r and r.ptr or nil
    end
    R.GetMediaItemInfo_Value = function(ptr, key)
        local r = find_item(ptr)
        if key == "D_POSITION" then return r.pos end
        if key == "D_LENGTH" then return r.len end
        return 0
    end
    R.SetMediaItemInfo_Value = function(ptr, key, v)
        local r = find_item(ptr)
        if key == "D_POSITION" then r.pos = v
        elseif key == "D_LENGTH" then r.len = v end
        bump()
        return true
    end
    R.GetSetMediaItemInfo_String = function(ptr, key, val, set)
        local r = find_item(ptr)
        if key == "GUID" then return true, r.guid end
        if key:sub(1, 6) == "P_EXT:" then
            local k = key:sub(7)
            if set then
                r.ext[k] = (val ~= "" and val or nil)
                bump()
                return true, val
            end
            local v = r.ext[k]
            return v ~= nil, v or ""
        end
        return false, ""
    end

    R.CountProjectMarkers = function()
        local nm, nr = 0, 0
        for i = 1, #S.marks do
            if S.marks[i].isrgn then nr = nr + 1 else nm = nm + 1 end
        end
        return nm + nr, nm, nr
    end
    R.GetNumRegionsOrMarkers = function() return #S.marks end
    R.EnumProjectMarkers3 = function(_, idx)
        local arr = sorted_marks()
        local m = arr[idx + 1]
        if not m then return 0, false, 0, 0, "", 0, 0 end
        return idx + 1, m.isrgn, m.pos, m.rgnend, m.name, m.id, m.color
    end
    R.EnumProjectMarkers2 = function(p, idx)
        local rv, isrgn, pos, rgnend, name, id = R.EnumProjectMarkers3(p, idx)
        return rv, isrgn, pos, rgnend, name, id
    end
    local function lowest_free(isrgn)
        local used = {}
        for i = 1, #S.marks do
            if S.marks[i].isrgn == isrgn then used[S.marks[i].id] = true end
        end
        local id = 1
        while used[id] do id = id + 1 end
        return id
    end
    R.AddProjectMarker2 = function(_, isrgn, pos, rgnend, name, wantidx, color)
        local id = lowest_free(isrgn)
        S.marks[#S.marks + 1] = {
            isrgn = isrgn, pos = pos, rgnend = isrgn and rgnend or pos,
            name = name or "", id = id, color = color or 0
        }
        bump()
        return id
    end
    R.AddProjectMarker = function(p, isrgn, pos, rgnend, name, wantidx)
        return R.AddProjectMarker2(p, isrgn, pos, rgnend, name, wantidx, 0)
    end
    R.DeleteProjectMarker = function(_, id, isrgn)
        for i = 1, #S.marks do
            local m = S.marks[i]
            if m.id == id and m.isrgn == isrgn then
                table.remove(S.marks, i)
                bump()
                return true
            end
        end
        return false
    end
    R.SetProjectMarkerByIndex2 = function(_, idx, isrgn, pos, rgnend, id, name, color, flags)
        local arr = sorted_marks()
        local m = arr[idx + 1]
        if not m then return false end
        m.isrgn = isrgn
        m.pos = pos
        m.rgnend = isrgn and rgnend or pos
        m.id = id
        if name ~= "" or ((flags or 0) & 1) ~= 0 then m.name = name end
        if color ~= 0 then m.color = color end
        bump()
        return true
    end
    R.SetProjectMarkerByIndex = function(p, idx, isrgn, pos, rgnend, id, name, color)
        return R.SetProjectMarkerByIndex2(p, idx, isrgn, pos, rgnend, id, name, color, 0)
    end
    R.SetProjectMarker3 = function(_, id, isrgn, pos, rgnend, name, color)
        for i = 1, #S.marks do
            local m = S.marks[i]
            if m.id == id and m.isrgn == isrgn then
                m.pos = pos; m.rgnend = isrgn and rgnend or pos
                if name ~= "" then m.name = name end
                if color ~= 0 then m.color = color end
                bump()
                return true
            end
        end
        return false
    end
    R.GetRegionOrMarker = function(_, idx)
        local arr = sorted_marks()
        return arr[idx + 1]
    end
    R.SetRegionOrMarkerInfo_Value = function(_, pm, key, v)
        if key == "I_CUSTOMCOLOR" then pm.color = v; bump(); return true end
        return false
    end

    R.Undo_BeginBlock2 = function() S.undo_block_depth = S.undo_block_depth + 1 end
    R.Undo_BeginBlock = function() R.Undo_BeginBlock2(proj) end
    R.Undo_EndBlock2 = function(_, desc)
        S.undo_block_depth = S.undo_block_depth - 1
        M.add_undo_point(desc)
    end
    R.Undo_EndBlock = function(desc, f) R.Undo_EndBlock2(proj, desc, f) end
    R.Undo_OnStateChange2 = function(_, desc) M.add_undo_point(desc) end
    R.Undo_OnStateChange = function(desc) M.add_undo_point(desc) end
    R.Undo_CanUndo2 = function()
        if S.undo_pos > 1 then return S.undo[S.undo_pos].desc end
        return nil
    end
    R.Undo_CanRedo2 = function()
        if S.undo_pos < #S.undo then return S.undo[S.undo_pos + 1].desc end
        return nil
    end
    R.Undo_DoUndo2 = function()
        if S.undo_pos <= 1 then return 0 end
        S.undo_pos = S.undo_pos - 1
        restore(S.undo[S.undo_pos].state)
        return 1
    end
    R.Undo_DoRedo2 = function()
        if S.undo_pos >= #S.undo then return 0 end
        S.undo_pos = S.undo_pos + 1
        restore(S.undo[S.undo_pos].state)
        return 1
    end

    R.GetSet_LoopTimeRange2 = function(_, set, isloop, s, e)
        local t = isloop and S.loop or S.ts
        if set then t[1], t[2] = s, e end
        return t[1], t[2]
    end
    R.GetSet_LoopTimeRange = function(set, isloop, s, e)
        return R.GetSet_LoopTimeRange2(proj, set, isloop, s, e)
    end
    R.Main_OnCommandEx = function(cmd)
        if cmd == 40200 then
            local s, e = S.ts[1], S.ts[2]
            local d = e - s
            if d <= 0 then return end
            for i = 1, #S.items do
                if S.items[i].pos >= s - 1e-9 then S.items[i].pos = S.items[i].pos + d end
            end
            if not S.lock_markers_on_insert then
                for i = 1, #S.marks do
                    local m = S.marks[i]
                    if m.pos >= s - 1e-9 then
                        m.pos = m.pos + d
                        m.rgnend = m.rgnend + d
                    end
                end
            end
            bump()
        else
            error("unexpected command " .. tostring(cmd))
        end
    end
    R.Main_OnCommand = function(cmd) R.Main_OnCommandEx(cmd, 0, proj) end

    R.ShowMessageBox = function(msg, title, typ)
        local ans = table.remove(S.msgbox_answers, 1)
        if ans == nil then
            -- defaults: OK/Yes
            if typ == 4 then ans = 6 elseif typ == 1 then ans = 1 else ans = 1 end
        end
        S.msgbox_log[#S.msgbox_log + 1] = { msg = msg, typ = typ, ans = ans, t = S.now }
        return ans
    end

    return R
end

return M
