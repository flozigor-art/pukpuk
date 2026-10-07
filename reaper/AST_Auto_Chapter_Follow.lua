--[[
  AST Auto Chapter Follow — v2.5
  ------------------------------
  Deterministic chapter follower + *** organizer.

  Core rules:
    * every REAPER region is a chapter;
    * chapter region = owned items of its active pack + 1.5 s on both sides
      (clamped at 0:00);
    * all project items participate; overlaps are one occupied timeline entity;
    * gap <= 10 s keeps/rejoins a pack; gap > 10 s splits it;
    * when one old chapter is split by a manual item move, the old region stays
      with stationary material; if everything moved, it stays with the earliest
      resulting old-owned pack;
    * detached old-owned items remember their chapter and rejoin if they come back;
    * when a detached part of one chapter is moved into another existing chapter,
      only that part changes ownership: the source region shrinks and the target
      region expands;
    * when two chapters' active packs become one <= 10 s pack the script asks to
      merge. Right after a manual move "No" undoes that move; otherwise (after
      recording, on startup, a bridging take) "No" leaves both chapters as they
      are and each region keeps covering only its own items;
    * a manual region drawn around exactly one detached old-owned pack adopts that
      pack as a new chapter, including after script/REAPER restart;
    * a truly empty/orphan region is deleted after two separate settled cycles;
    * regions longer than 59:45 are red; when shorter they return to default color;

  Layout safety (no item is ever placed on top of another item):
    * a reflow moves only whole physical packs, never a part of a pack;
    * EVERYTHING left of the chapter being placed takes part in the packing:
      other chapters, detached pieces, material without a region (for example a
      first chapter recorded before the first ***). Chapters keep >= 60 s between
      region edges; material physically connected (<= 10 s) to the chapter on
      its right is separated by 60 s as well (otherwise it would be glued into
      it); any other material keeps its original distance and only moves
      rigidly together with its right neighbour, and only as far as needed;
    * before anything is written, the planned layout is verified: if any pair of
      items that did not overlap before would overlap afterwards, nothing is done;
    * while recording, nothing at or after the point where recording started is
      ever moved; such a reflow waits until recording stops;
    * stars that sit inside the moving material travel with the item they resolve
      to (their meaning is preserved). Boundary stars of the current chapter sit
      on the fixed side and are never moved.

  *** rules:
    * markers < 5 s apart: delete the later one (timeline) silently; the kept one
      inherits the older insertion rank, so a pending role is never lost;
    * pending *** markers are ordered by insertion chronology, not timeline position;
      a marker restored by Undo keeps its old rank;
    * at most 3 pending *** markers; newest insertions beyond 3 are deleted;
    * marker in silence -> boundary is the next item start;
    * marker inside exactly one item and <= 15 s from that item's start -> the
      whole item belongs to the new chapter;
    * marker deeper than 15 s into an item, or inside 2+ simultaneous items -> BLOCK;
    * with A+B, finalize A->B once B chapter has >=3 items and >45 s occupied audio;
    * with A+B+C, finalize A->B immediately (maturity override);
    * while only A+B exist, moving the second marker before the first resolved
      boundary is a harmless pending edit: wait silently; with C the order must be
      A < B < C, otherwise BLOCK;
    * if A->B has no items, after recording warn and delete A;
    * hard gaps >10 s inside the chapter being finalized are silently reduced to 9 s;
    * finalized chapters are packed with >=60 s between regions;
    * A->B reuses an existing region only when that region's whole active pack lies
      inside A->B; pieces of other chapters inside A->B are transferred to it;
    * if there is no room before 0:00, never interrupt recording; after recording
      offer to insert +1 hour at project start (again after every recording stop
      while the problem persists), then rescan and continue;
    * a newly inserted valid *** that divides owned material of one existing region
      is a one-shot split command: old region stays left, a new "rename" region is
      created on the right, a 60 s chapter gap is established, and that triggering
      *** is consumed immediately without touching other *** markers. A split
      command that cannot be executed yet stays pending and is ignored by the
      A/B/C organizer;
    * for the current chapter, helper markers "40 min" and "50 min" are created
      at +40:00 and +50:00 from its resolved first item once those times are
      reached (during recording the live play position counts).

  Undo:
    * every script edit is one undo point named "Chapter automation: ...";
    * when the user undoes one of those points the script pauses until the user
      makes the next change, so it never immediately redoes what was undone.

  Persistence:
    * each item stores its historical chapter region ID in P_EXT:AST_CHAPTER_REGION.
      This survives script/REAPER restarts and makes large rigid moves deterministic.

  Project tabs:
    * one global watcher follows the active project tab;
    * switching tabs, opening a tab, or closing a tab fully reinitializes volatile
      runtime state from the currently active project.

  Notes:
    * no automation/envelope handling by design (the +1 h shift uses REAPER's own
      "insert empty space", so envelopes and tempo follow it);
    * ordinary project markers move with rigid chapter packages.
]]

------------------------------------------------------------
-- Settings
------------------------------------------------------------

local REGION_PAD            = 1.5
local PACK_GAP              = 10.0
local COMPACT_GAP           = 9.0
local LAYOUT_GAP            = 60.0
local PROJECT_SHIFT_SEC     = 3600.0

local STAR_NAME             = "***"
local NEW_REGION_NAME       = "rename"
local STAR_DUPLICATE_SEC    = 5.0
local STAR_ITEM_WINDOW_SEC  = 15.0
local MAX_PENDING_STARS     = 3

local HELPER_40_NAME        = "40 min"
local HELPER_50_NAME        = "50 min"
local HELPER_40_SEC         = 40 * 60.0
local HELPER_50_SEC         = 50 * 60.0
local HELPER_SCAN_SEC       = 1.0
local HELPER_MATCH_SEC      = 8 * 60.0

local MATURE_MIN_ITEMS      = 3
local MATURE_AUDIO_SEC      = 45.0

local LONG_REGION_SEC       = 59 * 60 + 45 -- 59:45
local LONG_REGION_RED       = (reaper.ColorToNative(255, 0, 0) | 0x1000000)

local POLL_INTERVAL         = 0.20
local SETTLE_SEC            = 0.30
local RECORD_SCAN_SEC       = 0.75
local STAR_SCAN_SEC         = 0.75
local GESTURE_RELEASE_SEC   = 0.18
local ORPHAN_CONFIRMATIONS  = 2
local REC_GUARD_MARGIN      = 1.0
local MAX_PASSES_PER_CYCLE  = 8

local EPS                   = 0.0005
local MOVE_EPS              = 0.002
local STAR_RESTORE_EPS      = 0.001

local ITEM_EXT_KEY          = "P_EXT:AST_CHAPTER_REGION"
local UNDO_TAG              = "Chapter automation: "
local MSG_TITLE             = "Chapter automation"

local proj = reaper.EnumProjects(-1, "")
if not proj then return end

------------------------------------------------------------
-- Singleton / toolbar
------------------------------------------------------------

-- One watcher owns the whole REAPER instance. It follows the active project tab
-- and reinitializes from scratch whenever the active tab or the set of open tabs
-- changes. This avoids stale ReaProject* pointers after tabs are closed.
local SYNC_SECTION = "AST_ChapterAutomation"
local OWNER_KEY = "chapter_final_owner_global"
local LEGACY_OWNER_KEY = "chapter_follower_owner_global"

local INSTANCE_TOKEN = string.format(
    "final-v2.5@%.9f@%s",
    reaper.time_precise(),
    (tostring({}):gsub("table: ", ""))
)
local LEGACY_EVICT_TOKEN = "FINAL@" .. INSTANCE_TOKEN

local function project_scoped_key(prefix, p)
    local key = tostring(p):gsub("[^%w_%-]", "_")
    return prefix .. key
end

-- Evict old project-scoped instances that may already be running in any
-- currently open tab. Current instances coordinate through OWNER_KEY instead.
local function evict_project_scoped_instances()
    local i = 0
    while true do
        local p = reaper.EnumProjects(i, "")
        if not p then break end
        reaper.SetExtState(
            SYNC_SECTION,
            project_scoped_key("chapter_final_owner_", p),
            LEGACY_EVICT_TOKEN,
            false
        )
        reaper.SetExtState(
            SYNC_SECTION,
            project_scoped_key("chapter_follower_owner_", p),
            LEGACY_EVICT_TOKEN,
            false
        )
        i = i + 1
    end
end

reaper.SetExtState(SYNC_SECTION, OWNER_KEY, INSTANCE_TOKEN, false)
reaper.SetExtState(SYNC_SECTION, LEGACY_OWNER_KEY, LEGACY_EVICT_TOKEN, false)
evict_project_scoped_instances()

local _, _, section_id, cmd_id = reaper.get_action_context()
local function set_toggle(v)
    if section_id and cmd_id and cmd_id ~= 0 then
        reaper.SetToggleCommandState(section_id, cmd_id, v)
        reaper.RefreshToolbar2(section_id, cmd_id)
    end
end

set_toggle(1)
reaper.atexit(function()
    local owner = reaper.GetExtState(SYNC_SECTION, OWNER_KEY)
    -- A newer instance that took over owns the toolbar state now; switching the
    -- button off here would show "off" while that instance is running.
    if owner == INSTANCE_TOKEN or owner == "" then set_toggle(0) end
    if owner == INSTANCE_TOKEN then
        reaper.DeleteExtState(SYNC_SECTION, OWNER_KEY, false)
    end
    if reaper.GetExtState(SYNC_SECTION, LEGACY_OWNER_KEY) == LEGACY_EVICT_TOKEN then
        reaper.DeleteExtState(SYNC_SECTION, LEGACY_OWNER_KEY, false)
    end
end)

------------------------------------------------------------
-- Runtime state
------------------------------------------------------------

local prev_snap = nil
local orphan_seen = {}      -- rid -> { count = n, cycle = id of last counted cycle }
local cycle_id = 0

-- Marker insertion chronology is runtime state. Existing markers found on script
-- startup are seeded in timeline order; every marker observed later is appended.
-- This is deliberately separate from snap.stars, which remains timeline-sorted
-- for spatial calculations.
local star_birth_seq = {}
local star_birth_counter = 0
local star_last_pos = {}
local star_tomb = {}        -- id -> { seq, pos } of stars that disappeared (Undo may restore them)
local local_split_pending = {}

local last_state = reaper.GetProjectStateChangeCount(proj)
local observed_state = last_state
local state_changed_at = reaper.time_precise()
local last_poll = 0
local last_record_scan = -math.huge
local last_star_scan = -math.huge
local last_helper_scan = -math.huge

-- Recording bookkeeping. rec_start_pos is where the running recording began;
-- nothing at or after it may be moved while recording. record_session counts
-- finished recordings so "after recording" offers are repeated once per stop.
local was_recording = false
local rec_start_pos = nil
local record_session = 0

-- A follow-up cycle requested by the previous one (orphan confirmation).
local recheck_at = nil

local prompted_collisions = {}
local last_blocker_signature = ""
local last_split_block_signature = ""
local last_space_signature = ""
local last_empty_signature = ""
local last_multi_signature = ""
local last_unsafe_signature = ""

local HAS_JS_MOUSE = reaper.APIExists and reaper.APIExists("JS_Mouse_GetState")
local MOUSE_BUTTON_MASK = 1 | 2 | 64
local mouse_was_down = false
local last_mouse_release = -math.huge

------------------------------------------------------------
-- Small helpers
------------------------------------------------------------

local function abs(x) return x < 0 and -x or x end
local function min(a, b) return a < b and a or b end
local function max(a, b) return a > b and a or b end

local function is_recording()
    if reaper.GetPlayStateEx then
        return (reaper.GetPlayStateEx(proj) & 4) ~= 0
    end
    return (reaper.GetPlayState() & 4) ~= 0
end

local function play_position()
    if reaper.GetPlayPositionEx then return reaper.GetPlayPositionEx(proj) end
    return reaper.GetPlayPosition()
end

local function cursor_position()
    if reaper.GetCursorPositionEx then return reaper.GetCursorPositionEx(proj) end
    return reaper.GetCursorPosition()
end

-- Earliest timeline position the running recording can write to. The edit
-- cursor stays at the record start while recording; the first observed play
-- position covers pre-roll; with repeat on, loop recording may wrap back to the
-- loop start; in auto-punch mode the take starts at the time selection.
local function observe_recording_start()
    local p = min(cursor_position(), play_position())
    local getloop = reaper.GetSet_LoopTimeRange2
    if getloop and reaper.GetSetRepeatEx and reaper.GetSetRepeatEx(proj, -1) == 1 then
        local ls, le = getloop(proj, false, true, 0, 0, false)
        if le > ls + EPS then p = min(p, ls) end
    end
    if getloop and reaper.GetToggleCommandStateEx
        and reaper.GetToggleCommandStateEx(0, 40076) == 1 then -- time selection auto punch
        local ts, te = getloop(proj, false, false, 0, 0, false)
        if te > ts + EPS then p = min(p, ts) end
    end
    return max(0, p)
end

-- Nothing whose current extent reaches this position may move while recording.
local function recording_guard()
    if not is_recording() then return nil end
    if not rec_start_pos then rec_start_pos = observe_recording_start() end
    return rec_start_pos - REC_GUARD_MARGIN
end

local function mouse_gesture_active(now)
    if not HAS_JS_MOUSE then return false end
    local down = reaper.JS_Mouse_GetState(MOUSE_BUTTON_MASK) ~= 0
    if down then
        mouse_was_down = true
        return true
    end
    if mouse_was_down then
        mouse_was_down = false
        last_mouse_release = now
        return true
    end
    return now - last_mouse_release < GESTURE_RELEASE_SEC
end

local function interval_overlap(a_s, a_e, b_s, b_e)
    local v = min(a_e, b_e) - max(a_s, b_s)
    return v > 0 and v or 0
end

local function interval_distance(a_s, a_e, b_s, b_e)
    if a_e < b_s then return b_s - a_e end
    if a_s > b_e then return a_s - b_e end
    return 0
end

local function get_item_guid(item)
    local ok, guid = reaper.GetSetMediaItemInfo_String(item, "GUID", "", false)
    if ok and guid and guid ~= "" then return guid end
    return tostring(item)
end

local function get_item_home(item)
    local ok, v = reaper.GetSetMediaItemInfo_String(item, ITEM_EXT_KEY, "", false)
    if not ok or not v or v == "" then return nil end
    return tonumber(v)
end

local function set_item_home(item, rid)
    local value = rid and tostring(rid) or ""
    reaper.GetSetMediaItemInfo_String(item, ITEM_EXT_KEY, value, true)
end

local function sort_items(items)
    table.sort(items, function(a, b)
        if abs(a.s - b.s) <= EPS then
            if abs(a.e - b.e) <= EPS then return a.guid < b.guid end
            return a.e < b.e
        end
        return a.s < b.s
    end)
end

local function table_count(t)
    local n = 0
    for _ in pairs(t) do n = n + 1 end
    return n
end

local function is_star_name(name)
    return name == STAR_NAME or (name and name:match("^%s*(.-)%s*$") == STAR_NAME)
end

-- Return the currently active project and a stable identity signature for the
-- set of open project tabs. Sorting makes tab reordering irrelevant: only
-- opening/closing tabs changes the signature.
local function get_project_context()
    local active = reaper.EnumProjects(-1, "")
    if not active then return nil, "" end

    local ids = {}
    local i = 0
    while true do
        local p = reaper.EnumProjects(i, "")
        if not p then break end
        ids[#ids + 1] = tostring(p)
        i = i + 1
    end
    table.sort(ids)
    return active, table.concat(ids, "|")
end

local function ids_signature(ids)
    local copy = {}
    for i = 1, #ids do copy[i] = ids[i] end
    table.sort(copy)
    local out = {}
    for i = 1, #copy do out[i] = tostring(copy[i]) end
    return table.concat(out, ",")
end

local function is_own_undo(desc)
    return type(desc) == "string" and desc:sub(1, #UNDO_TAG) == UNDO_TAG
end

-- True right after the user undid one of this script's undo points.
local function own_action_on_redo_stack()
    if not reaper.Undo_CanRedo2 then return false end
    return is_own_undo(reaper.Undo_CanRedo2(proj))
end

local function own_action_on_undo_stack()
    if not reaper.Undo_CanUndo2 then return false end
    return is_own_undo(reaper.Undo_CanUndo2(proj))
end

local function message(text)
    reaper.ShowMessageBox(text, MSG_TITLE, 0)
end

------------------------------------------------------------
-- Project marker/region helpers
------------------------------------------------------------

local function find_entry_by_id(id, want_region)
    local _, nm, nr = reaper.CountProjectMarkers(proj)
    local total = nm + nr
    for i = 0, total - 1 do
        local rv, isrgn, s, e, name, found_id, color = reaper.EnumProjectMarkers3(proj, i)
        if rv > 0 and isrgn == want_region and found_id == id then
            return {
                enum_idx = i, isrgn = isrgn, s = s, e = e,
                name = name, id = found_id, color = color
            }
        end
    end
    return nil
end

local function write_entry_by_id(id, isrgn, s, e, name, color)
    local x = find_entry_by_id(id, isrgn)
    if not x then return false end
    return reaper.SetProjectMarkerByIndex2(
        proj, x.enum_idx, isrgn, s, isrgn and e or 0,
        id, name or x.name or "", color or x.color or 0, 0
    )
end

local function set_region_default_or_red(rid, make_red)
    local x = find_entry_by_id(rid, true)
    if not x then return false end

    if reaper.GetRegionOrMarker and reaper.SetRegionOrMarkerInfo_Value then
        local pm = reaper.GetRegionOrMarker(proj, x.enum_idx, "")
        if pm then
            reaper.SetRegionOrMarkerInfo_Value(
                proj, pm, "I_CUSTOMCOLOR", make_red and LONG_REGION_RED or 0
            )
            return true
        end
    end

    -- Fallback. On modern REAPER the pointer API above is preferred because it
    -- can explicitly clear I_CUSTOMCOLOR back to zero.
    return reaper.SetProjectMarkerByIndex2(
        proj, x.enum_idx, true, x.s, x.e, x.id, x.name,
        make_red and LONG_REGION_RED or 0, 0
    )
end

local function add_chapter_region(s, e)
    local rid = reaper.AddProjectMarker2(proj, true, max(0, s), e, NEW_REGION_NAME, -1, 0)
    if not rid or rid < 0 then return nil end
    return rid
end

------------------------------------------------------------
-- *** insertion chronology
------------------------------------------------------------

local function birth_less(a, b)
    local sa = star_birth_seq[a.id] or math.huge
    local sb = star_birth_seq[b.id] or math.huge
    if sa == sb then return a.id < b.id end
    return sa < sb
end

local function sync_star_birth_order(snap)
    local present = {}
    for i = 1, #snap.stars do present[snap.stars[i].id] = snap.stars[i] end

    -- A marker that disappears keeps a tombstone: if Undo restores the very same
    -- marker (same ID, same position) it gets its old rank back instead of
    -- becoming the newest pending marker.
    for id, seq in pairs(star_birth_seq) do
        if not present[id] then
            star_tomb[id] = { seq = seq, pos = star_last_pos[id] }
            star_birth_seq[id] = nil
            star_last_pos[id] = nil
        end
    end

    local newcomers = {}
    for i = 1, #snap.stars do
        local st = snap.stars[i]
        if not star_birth_seq[st.id] then
            local tomb = star_tomb[st.id]
            if tomb and tomb.pos and abs(tomb.pos - st.pos) <= STAR_RESTORE_EPS then
                star_birth_seq[st.id] = tomb.seq
            else
                newcomers[#newcomers + 1] = st
            end
            star_tomb[st.id] = nil
        end
    end

    if #newcomers > 0 then
        if star_birth_counter == 0 and not prev_snap then
            -- We cannot reconstruct pre-script insertion history. Timeline order is
            -- the safest seed for already-existing pending chapter markers.
            table.sort(newcomers, function(a, b)
                if abs(a.pos - b.pos) <= EPS then return a.id < b.id end
                return a.pos < b.pos
            end)
        else
            -- Normally there is exactly one newcomer. If several appeared between
            -- scans, marker IDs are a deterministic fallback instead of timeline
            -- position, so an old-position edit cannot become the oldest pending *.
            table.sort(newcomers, function(a, b) return a.id < b.id end)
        end
        for i = 1, #newcomers do
            star_birth_counter = star_birth_counter + 1
            star_birth_seq[newcomers[i].id] = star_birth_counter
        end
    end

    for i = 1, #snap.stars do star_last_pos[snap.stars[i].id] = snap.stars[i].pos end

    snap.stars_by_birth = {}
    for i = 1, #snap.stars do snap.stars_by_birth[i] = snap.stars[i] end
    table.sort(snap.stars_by_birth, birth_less)
end

local function stars_in_birth_order(snap)
    return snap.stars_by_birth or snap.stars
end

-- Pending chapter markers for the A/B/C organizer: split commands that are still
-- waiting (blocked / no space / recording) are not chapter boundaries.
local function pending_stars(snap)
    local out = {}
    local ordered = stars_in_birth_order(snap)
    for i = 1, #ordered do
        if not local_split_pending[ordered[i].id] then out[#out + 1] = ordered[i] end
    end
    return out
end

------------------------------------------------------------
-- Frozen snapshot
------------------------------------------------------------

local function read_snapshot()
    local snap = {
        items = {},
        item_by_guid = {},
        regions = {},
        region_map = {},
        stars = {},
        star_map = {},
        markers = {},
        entries = {}
    }

    local nitems = reaper.CountMediaItems(proj)
    for i = 0, nitems - 1 do
        local item = reaper.GetMediaItem(proj, i)
        if item then
            local s = reaper.GetMediaItemInfo_Value(item, "D_POSITION")
            local len = reaper.GetMediaItemInfo_Value(item, "D_LENGTH")
            local guid = get_item_guid(item)
            local d = {
                item = item,
                guid = guid,
                s = s,
                e = s + max(0, len),
                len = max(0, len),
                home = get_item_home(item),
                moved = false,
                stationary = false
            }
            if prev_snap and prev_snap.item_by_guid[guid] then
                local old = prev_snap.item_by_guid[guid]
                d.stationary = abs(old.s - d.s) <= MOVE_EPS and abs(old.e - d.e) <= MOVE_EPS
                d.moved = not d.stationary
            end
            snap.items[#snap.items + 1] = d
            snap.item_by_guid[guid] = d
        end
    end
    sort_items(snap.items)

    local _, nm, nr = reaper.CountProjectMarkers(proj)
    local total = nm + nr
    for i = 0, total - 1 do
        local rv, isrgn, s, e, name, id, color = reaper.EnumProjectMarkers3(proj, i)
        if rv > 0 then
            local entry = {
                enum_idx = i, isrgn = isrgn, s = s, e = e,
                name = name, id = id, color = color
            }
            snap.entries[#snap.entries + 1] = entry
            if isrgn then
                snap.regions[#snap.regions + 1] = entry
                snap.region_map[id] = entry
            elseif is_star_name(name) then
                local st = { id = id, pos = s, color = color, enum_idx = i, name = name }
                snap.stars[#snap.stars + 1] = st
                snap.star_map[id] = st
            else
                snap.markers[#snap.markers + 1] = entry
            end
        end
    end

    table.sort(snap.regions, function(a, b)
        if abs(a.s - b.s) <= EPS then return a.id < b.id end
        return a.s < b.s
    end)
    table.sort(snap.stars, function(a, b)
        if abs(a.pos - b.pos) <= EPS then return a.id < b.id end
        return a.pos < b.pos
    end)
    table.sort(snap.markers, function(a, b)
        if abs(a.s - b.s) <= EPS then return a.id < b.id end
        return a.s < b.s
    end)

    sync_star_birth_order(snap)
    return snap
end

local function commit_prev_snapshot()
    prev_snap = read_snapshot()
end

------------------------------------------------------------
-- Physical occupied components (all tracks, overlap = one entity)
------------------------------------------------------------

local function build_components(items, max_gap)
    max_gap = max_gap or PACK_GAP
    if #items == 0 then return {} end

    local src = {}
    for i = 1, #items do src[i] = items[i] end
    sort_items(src)

    local out = {}
    local c = { s = src[1].s, e = src[1].e, items = { src[1] } }

    for i = 2, #src do
        local d = src[i]
        local gap = d.s - c.e
        if gap <= max_gap + EPS then
            c.items[#c.items + 1] = d
            if d.e > c.e then c.e = d.e end
        else
            out[#out + 1] = c
            c = { s = d.s, e = d.e, items = { d } }
        end
    end
    out[#out + 1] = c
    return out
end

local function occupied_duration(items)
    if #items == 0 then return 0 end
    local comps = build_components(items, 0)
    local sum = 0
    for i = 1, #comps do sum = sum + max(0, comps[i].e - comps[i].s) end
    return sum
end

local function component_home_stats(c)
    local st = {}
    for i = 1, #c.items do
        local d = c.items[i]
        if d.home then
            local x = st[d.home]
            if not x then
                x = { count = 0, dur = 0, stationary = 0, moved = 0, first = math.huge }
                st[d.home] = x
            end
            x.count = x.count + 1
            x.dur = x.dur + d.len
            if d.stationary then x.stationary = x.stationary + 1 end
            if d.moved then x.moved = x.moved + 1 end
            if d.s < x.first then x.first = d.s end
        end
    end
    return st
end

-- Span of the items of component c that are owned by rid.
local function owned_span(c, rid)
    local s, e = nil, nil
    for i = 1, #c.items do
        local d = c.items[i]
        if d.home == rid then
            if not s or d.s < s then s = d.s end
            if not e or d.e > e then e = d.e end
        end
    end
    return s, e
end

------------------------------------------------------------
-- Persistent ownership bootstrap / active component selection
------------------------------------------------------------

local function clear_stale_homes(snap)
    local changed = false
    for i = 1, #snap.items do
        local d = snap.items[i]
        if d.home and not snap.region_map[d.home] then
            set_item_home(d.item, nil)
            d.home = nil
            changed = true
        end
    end
    return changed
end

local function count_homes_by_region(snap)
    local out = {}
    for i = 1, #snap.items do
        local rid = snap.items[i].home
        if rid and snap.region_map[rid] then out[rid] = (out[rid] or 0) + 1 end
    end
    return out
end

local function bootstrap_unowned_regions(snap, components)
    local home_counts = count_homes_by_region(snap)
    local claimed_component = {}

    -- Components already containing historical ownership are not bootstrap targets.
    for ci = 1, #components do
        local st = component_home_stats(components[ci])
        if table_count(st) > 0 then claimed_component[ci] = true end
    end

    local changed = false
    for ri = 1, #snap.regions do
        local r = snap.regions[ri]
        if not home_counts[r.id] then
            local core_s = r.s + REGION_PAD
            local core_e = r.e - REGION_PAD
            if core_e <= core_s + EPS then core_s, core_e = r.s, r.e end

            local best_ci, best_overlap, best_dist = nil, -1, math.huge
            for ci = 1, #components do
                if not claimed_component[ci] then
                    local c = components[ci]
                    local ov = interval_overlap(c.s, c.e, core_s, core_e)
                    local dist = interval_distance(c.s, c.e, core_s, core_e)
                    if ov > EPS or dist <= PACK_GAP + EPS then
                        if ov > best_overlap + EPS
                            or (abs(ov - best_overlap) <= EPS and dist < best_dist - EPS)
                            or (abs(ov - best_overlap) <= EPS and abs(dist - best_dist) <= EPS
                                and (not best_ci or c.s < components[best_ci].s)) then
                            best_ci, best_overlap, best_dist = ci, ov, dist
                        end
                    end
                end
            end

            if best_ci then
                claimed_component[best_ci] = true
                local c = components[best_ci]
                local seeded = 0
                for j = 1, #c.items do
                    local d = c.items[j]
                    -- Bootstrap is deliberately geometry-conservative. The region
                    -- core is the historical item span; do not let a <=10 s physical
                    -- connection on first run swallow fresh material beyond that core
                    -- (especially across a live *** before P_EXT history exists).
                    local touches_core = interval_overlap(d.s, d.e, core_s, core_e) > EPS
                        or (d.s >= core_s - EPS and d.s <= core_e + EPS)
                    if not d.home and touches_core then
                        set_item_home(d.item, r.id)
                        d.home = r.id
                        seeded = seeded + 1
                        changed = true
                    end
                end
                if seeded > 0 then
                    home_counts[r.id] = seeded
                    orphan_seen[r.id] = nil
                end
            end
        end
    end
    return changed
end

local function choose_active_components(snap, components)
    local by_region = {}
    for ci = 1, #components do
        local c = components[ci]
        local st = component_home_stats(c)
        c.home_stats = st
        for rid, x in pairs(st) do
            if snap.region_map[rid] then
                local arr = by_region[rid]
                if not arr then arr = {}; by_region[rid] = arr end
                arr[#arr + 1] = { ci = ci, c = c, stat = x }
            end
        end
    end

    local active = {}
    for ri = 1, #snap.regions do
        local region = snap.regions[ri]
        local rid = region.id
        local arr = by_region[rid]
        if arr and #arr > 0 then
            local stationary = {}
            local any_moved = false
            for i = 1, #arr do
                if arr[i].stat.stationary > 0 then stationary[#stationary + 1] = arr[i] end
                if arr[i].stat.moved > 0 then any_moved = true end
            end

            local pool
            if any_moved then
                -- During the actual gesture the old rule is authoritative: keep
                -- the region on stationary material; if everything moved, keep the
                -- earliest resulting pack.
                pool = #stationary > 0 and stationary or arr
                table.sort(pool, function(a, b)
                    if abs(a.c.s - b.c.s) <= EPS then return a.ci < b.ci end
                    return a.c.s < b.c.s
                end)
            else
                -- Once the gesture has settled (and after restart), every item is
                -- stationary again. The persisted region geometry is then the only
                -- reliable record of which same-owned pack was active. Without this
                -- anchor a later manual region insertion could make the old chapter
                -- jump to an earlier detached pack.
                local core_s = region.s + REGION_PAD
                local core_e = region.e - REGION_PAD
                if core_e <= core_s + EPS then core_s, core_e = region.s, region.e end

                pool = arr
                table.sort(pool, function(a, b)
                    local a_overlap = interval_overlap(a.c.s, a.c.e, core_s, core_e)
                    local b_overlap = interval_overlap(b.c.s, b.c.e, core_s, core_e)
                    if abs(a_overlap - b_overlap) > EPS then return a_overlap > b_overlap end

                    local a_dist = interval_distance(a.c.s, a.c.e, core_s, core_e)
                    local b_dist = interval_distance(b.c.s, b.c.e, core_s, core_e)
                    if abs(a_dist - b_dist) > EPS then return a_dist < b_dist end

                    local region_mid = (core_s + core_e) * 0.5
                    local a_mid_dist = abs((a.c.s + a.c.e) * 0.5 - region_mid)
                    local b_mid_dist = abs((b.c.s + b.c.e) * 0.5 - region_mid)
                    if abs(a_mid_dist - b_mid_dist) > EPS then return a_mid_dist < b_mid_dist end

                    if abs(a.c.s - b.c.s) <= EPS then return a.ci < b.ci end
                    return a.c.s < b.c.s
                end)
            end
            active[rid] = pool[1].ci
        end
    end
    return active
end

------------------------------------------------------------
-- Manual region adoption for a detached old-owned pack
------------------------------------------------------------

local function find_manual_region_adoptions(snap, components, active)
    local home_counts = count_homes_by_region(snap)
    local claimed_component = {}
    local plans = {}

    for ri = 1, #snap.regions do
        local r = snap.regions[ri]
        if not home_counts[r.id] then
            local touching = {}
            for ci = 1, #components do
                local c = components[ci]
                if c.e > r.s + EPS and c.s < r.e - EPS then
                    touching[#touching + 1] = ci
                end
            end

            -- Manual intent is unambiguous only when the new region fully wraps
            -- exactly one physical pack. A partial/compound region is left alone
            -- for the ordinary orphan rules instead of guessing destructively.
            if #touching == 1 then
                local ci = touching[1]
                local c = components[ci]
                local fully_wrapped = c.s >= r.s - EPS and c.e <= r.e + EPS
                if fully_wrapped and not claimed_component[ci] then
                    local st = component_home_stats(c)
                    local sources = {}
                    for rid in pairs(st) do
                        if snap.region_map[rid] then sources[#sources + 1] = rid end
                    end
                    table.sort(sources)

                    if #sources == 1 then
                        local source = sources[1]
                        -- The wrapped pack must genuinely be detached. If it is the
                        -- source chapter's active pack, this is only a duplicate
                        -- region and must not steal the whole existing chapter.
                        if active[source] and active[source] ~= ci then
                            claimed_component[ci] = true
                            plans[#plans + 1] = {
                                target = r.id,
                                source = source,
                                ci = ci,
                                items = c.items
                            }
                        end
                    end
                end
            end
        end
    end

    return plans
end

local function adopt_manually_wrapped_detached_components(snap, components, active)
    local plans = find_manual_region_adoptions(snap, components, active)
    if #plans == 0 then return false end

    reaper.PreventUIRefresh(1)
    for i = 1, #plans do
        local p = plans[i]
        for j = 1, #p.items do
            set_item_home(p.items[j].item, p.target)
            p.items[j].home = p.target
        end
        orphan_seen[p.target] = nil
        orphan_seen[p.source] = nil
    end
    reaper.PreventUIRefresh(-1)
    return true
end

------------------------------------------------------------
-- Ownership transfer when a detached piece joins another chapter
------------------------------------------------------------

local function find_cross_chapter_transfer_plans(snap, components, active)
    local plans = {}

    for ci = 1, #components do
        local c = components[ci]
        local st = component_home_stats(c)
        local rids = {}
        for rid in pairs(st) do
            if snap.region_map[rid] then rids[#rids + 1] = rid end
        end
        table.sort(rids)

        if #rids >= 2 then
            local target = nil
            local target_count = 0
            local all_others_active_elsewhere = true

            for i = 1, #rids do
                local rid = rids[i]
                if active[rid] == ci then
                    target = rid
                    target_count = target_count + 1
                elseif not active[rid] then
                    all_others_active_elsewhere = false
                end
            end

            -- Exactly one region owns this component as its active pack. Every
            -- other ownership is therefore a detached piece from a chapter that
            -- still has its own active pack elsewhere. Rehome only those pieces;
            -- never delete either region and never move any item.
            if target_count == 1 and target and all_others_active_elsewhere then
                local moved_items = {}
                local sources = {}
                for i = 1, #rids do
                    if rids[i] ~= target then sources[rids[i]] = true end
                end
                for i = 1, #c.items do
                    local d = c.items[i]
                    if sources[d.home] then moved_items[#moved_items + 1] = d end
                end
                if #moved_items > 0 then
                    plans[#plans + 1] = {
                        target = target,
                        sources = sources,
                        ci = ci,
                        items = moved_items
                    }
                end
            end
        end
    end

    return plans
end

local function transfer_detached_items_between_chapters(snap, components, active)
    local plans = find_cross_chapter_transfer_plans(snap, components, active)
    if #plans == 0 then return false end

    reaper.PreventUIRefresh(1)
    for i = 1, #plans do
        local p = plans[i]
        for j = 1, #p.items do
            set_item_home(p.items[j].item, p.target)
            p.items[j].home = p.target
        end
        orphan_seen[p.target] = nil
        for rid in pairs(p.sources) do orphan_seen[rid] = nil end
    end
    reaper.PreventUIRefresh(-1)
    return true
end

------------------------------------------------------------
-- *** resolution
------------------------------------------------------------

local function resolve_star(snap, star, limit_pos)
    limit_pos = limit_pos or math.huge
    local under = {}

    for i = 1, #snap.items do
        local d = snap.items[i]
        -- An item that starts exactly at *** is active on the new side. If any
        -- other item is also sounding there, this is still an overlap blocker.
        if d.s <= star.pos + EPS and d.e > star.pos + EPS then
            under[#under + 1] = d
        end
    end

    if #under > 1 then
        return { status = "BLOCK", reason = "OVERLAP", star = star }
    end

    if #under == 1 then
        local d = under[1]
        local offs = max(0, star.pos - d.s)
        if offs <= STAR_ITEM_WINDOW_SEC + EPS then
            return {
                status = "VALID", kind = "ITEM", boundary = d.s,
                starter = d, star = star
            }
        end
        return {
            status = "BLOCK", reason = "DEEP_ITEM", offset = offs,
            item = d, star = star
        }
    end

    local next_start = nil
    local starter = nil
    for i = 1, #snap.items do
        local d = snap.items[i]
        if d.s >= star.pos - EPS and d.s < limit_pos - EPS then
            if not next_start or d.s < next_start - EPS then
                next_start = d.s
                starter = d
            end
        end
    end

    if next_start then
        return {
            status = "VALID", kind = "GAP", boundary = next_start,
            starter = starter, star = star
        }
    end

    if limit_pos < math.huge then
        return {
            status = "VALID", kind = "EMPTY", boundary = star.pos,
            starter = nil, star = star
        }
    end

    return { status = "WAIT", reason = "NO_NEXT_ITEM", star = star }
end

-- Unlimited resolution of every star, cached per snapshot.
local function star_resolutions(snap)
    if not snap.star_res then
        snap.star_res = {}
        for i = 1, #snap.stars do
            local st = snap.stars[i]
            snap.star_res[st.id] = resolve_star(snap, st, math.huge)
        end
    end
    return snap.star_res
end

local function star_blocks_acquisition(snap, anchor, item)
    local resolved = star_resolutions(snap)
    for i = 1, #snap.stars do
        local st = snap.stars[i]
        local res = resolved[st.id]
        local boundary = nil
        if res.status == "VALID" then boundary = res.boundary end

        if boundary then
            if anchor.e <= boundary + EPS and item.s >= boundary - EPS then
                return true
            end
            if anchor.s >= boundary - EPS and item.e <= boundary + EPS then
                return true
            end
        elseif res.status == "BLOCK" then
            -- Conservative around an ambiguous live boundary: never acquire fresh
            -- material through it. Historical ownership is intentionally preserved.
            if anchor.e <= st.pos + EPS and item.e > st.pos + EPS then
                return true
            end
            if anchor.s >= st.pos - EPS and item.s < st.pos - EPS then
                return true
            end
        end
    end
    return false
end

------------------------------------------------------------
-- Star sanitation
------------------------------------------------------------

local function new_star_id_set(snap)
    local out = {}
    if not prev_snap then return out end
    for i = 1, #snap.stars do
        local st = snap.stars[i]
        if not prev_snap.star_map[st.id] then out[st.id] = true end
    end
    return out
end

local function delete_star_markers(ids)
    if #ids == 0 then return false end
    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)
    for i = 1, #ids do
        reaper.DeleteProjectMarker(proj, ids[i], false)
        local_split_pending[ids[i]] = nil
    end
    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. "remove extra *** marker", -1)
    return true
end

local function enforce_star_capacity(snap)
    if #snap.stars <= MAX_PENDING_STARS then return false end

    local by_birth = {}
    for i = 1, #snap.stars do by_birth[i] = snap.stars[i] end
    table.sort(by_birth, birth_less)

    local to_delete = {}
    for i = #by_birth, MAX_PENDING_STARS + 1, -1 do
        to_delete[#to_delete + 1] = by_birth[i].id
    end
    return delete_star_markers(to_delete)
end

local function sanitize_stars(snap)
    if #snap.stars == 0 then return false end
    local to_delete = {}
    local kept = {}

    for i = 1, #snap.stars do
        local st = snap.stars[i]
        local prev = kept[#kept]
        if prev and (st.pos - prev.pos) < STAR_DUPLICATE_SEC - EPS then
            -- The later one on the timeline goes. If it was the older insertion,
            -- the survivor inherits that rank so A/B/C roles do not shift.
            to_delete[st.id] = true
            local sp = star_birth_seq[prev.id]
            local ss = star_birth_seq[st.id]
            if ss and (not sp or ss < sp) then star_birth_seq[prev.id] = ss end
        else
            kept[#kept + 1] = st
        end
    end

    if #kept > MAX_PENDING_STARS then
        local by_birth = {}
        for i = 1, #kept do by_birth[i] = kept[i] end
        table.sort(by_birth, birth_less)
        -- Capacity is about insertion chronology, not timeline chronology. Preserve
        -- the three oldest pending markers and reject every newest excess marker.
        for i = #by_birth, MAX_PENDING_STARS + 1, -1 do
            to_delete[by_birth[i].id] = true
        end
    end

    local ids = {}
    for id in pairs(to_delete) do ids[#ids + 1] = id end
    table.sort(ids)
    return delete_star_markers(ids)
end

------------------------------------------------------------
-- Collision: two existing chapter ownerships became one <=10 s component
------------------------------------------------------------

local function previous_owned_span_for_current_component(c, rid)
    if not prev_snap then return nil end
    local ps, pe = nil, nil
    for i = 1, #c.items do
        local d = c.items[i]
        if d.home == rid then
            local old = prev_snap.item_by_guid[d.guid]
            if not old then return nil end
            if not ps or old.s < ps then ps = old.s end
            if not pe or old.e > pe then pe = old.e end
        end
    end
    if not ps then return nil end
    return ps, pe
end

local function collision_is_new_proximity(c, rids)
    if not prev_snap then return true end

    -- A fresh collision is one where at least one pair of chapter-owned material
    -- was genuinely farther than 10 s before this user action and is connected
    -- now. A rigid move of the whole project preserves all previous distances and
    -- must never be interpreted as a merge gesture.
    for i = 1, #rids - 1 do
        local a_s, a_e = previous_owned_span_for_current_component(c, rids[i])
        if not a_s then return true end
        for j = i + 1, #rids do
            local b_s, b_e = previous_owned_span_for_current_component(c, rids[j])
            if not b_s then return true end
            if interval_distance(a_s, a_e, b_s, b_e) > PACK_GAP + EPS then
                return true
            end
        end
    end
    return false
end

-- Components that hold owned material of two or more chapters (transfers of
-- detached pieces have already been applied, so these are real collisions).
local function collision_list(snap, components)
    local out = {}
    for ci = 1, #components do
        local c = components[ci]
        local st = component_home_stats(c)
        local rids = {}
        local any_moved = false
        for rid, x in pairs(st) do
            if snap.region_map[rid] then
                rids[#rids + 1] = rid
                if x.moved > 0 then any_moved = true end
            end
        end
        if #rids >= 2 then
            table.sort(rids)
            out[#out + 1] = {
                ci = ci, c = c, stats = st, rids = rids,
                sig = ids_signature(rids),
                fresh = any_moved and collision_is_new_proximity(c, rids)
            }
        end
    end
    return out
end

local function choose_merge_survivor(snap, collision)
    local stationary = {}
    for _, rid in ipairs(collision.rids) do
        local x = collision.stats[rid]
        if x and x.stationary > 0 then stationary[#stationary + 1] = rid end
    end

    local pool = #stationary > 0 and stationary or collision.rids
    table.sort(pool, function(a, b)
        local ra, rb = snap.region_map[a], snap.region_map[b]
        local sa = (collision.stats[a] and collision.stats[a].first) or (ra and ra.s) or math.huge
        local sb = (collision.stats[b] and collision.stats[b].first) or (rb and rb.s) or math.huge
        if abs(sa - sb) <= EPS then return a < b end
        return sa < sb
    end)
    return pool[1]
end

local function rollback_user_move()
    -- Never undo one of our own edits by mistake.
    if own_action_on_undo_stack() then return false end
    local ok = reaper.Undo_DoUndo2(proj)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    return ok ~= 0
end

local function merge_collision(snap, collision)
    local survivor = choose_merge_survivor(snap, collision)
    if not survivor then return false end

    local losing = {}
    for _, rid in ipairs(collision.rids) do
        if rid ~= survivor then losing[rid] = true end
    end

    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)

    for i = 1, #snap.items do
        local d = snap.items[i]
        if losing[d.home] then
            set_item_home(d.item, survivor)
            d.home = survivor
        end
    end
    for rid in pairs(losing) do
        reaper.DeleteProjectMarker(proj, rid, true)
        orphan_seen[rid] = nil
    end

    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. "merge chapter regions", -1)
    return true
end

local function handle_collisions(snap, components, allow_prompts)
    local list = collision_list(snap, components)

    local live = {}
    for i = 1, #list do live[list[i].sig] = true end
    for sig in pairs(prompted_collisions) do
        if not live[sig] then prompted_collisions[sig] = nil end
    end

    -- Unresolved collisions are not blocking: acquisition skips such a pack and
    -- each region keeps covering only its own items until the user decides.
    if #list == 0 or not allow_prompts or is_recording() then return false end

    for i = 1, #list do
        local col = list[i]
        if not prompted_collisions[col.sig] then
            prompted_collisions[col.sig] = true
            local can_rollback = col.fresh and not own_action_on_undo_stack()

            local text = "Айтемы двух глав оказались ближе 10 секунд и теперь образуют один физический пакет.\n\n" ..
                "Объединить главы?\n\n" ..
                "Да — объединить главы и удалить старый лишний регион.\n"
            if can_rollback then
                text = text .. "Нет — отменить последнее пользовательское перемещение."
            else
                text = text .. "Нет — оставить как есть (каждый регион остаётся на своих айтемах; " ..
                    "разведите главы вручную)."
            end

            local result = reaper.ShowMessageBox(text, MSG_TITLE, 4)
            if result == 6 then
                -- Re-read after the modal dialog and merge only if the very same
                -- collision still exists.
                local fresh = read_snapshot()
                local comps = build_components(fresh.items, PACK_GAP)
                local again = collision_list(fresh, comps)
                for j = 1, #again do
                    if again[j].sig == col.sig then
                        return merge_collision(fresh, again[j])
                    end
                end
                return false
            elseif can_rollback then
                if rollback_user_move() then
                    commit_prev_snapshot()
                    return true
                end
            end
            return false
        end
    end
    return false
end

------------------------------------------------------------
-- Acquisition of fresh/unowned items into one existing chapter
------------------------------------------------------------

local function acquire_unowned_items(snap, components, active)
    local changed = false
    local active_ci_to_rid = {}
    for rid, ci in pairs(active) do
        if active_ci_to_rid[ci] == nil then
            active_ci_to_rid[ci] = rid
        else
            active_ci_to_rid[ci] = false -- collision; never guess an owner
        end
    end

    for ci = 1, #components do
        local rid = active_ci_to_rid[ci]
        if rid then
            local c = components[ci]
            local owned_s, owned_e = owned_span(c, rid)
            local anchor = {
                s = owned_s or c.s,
                e = owned_e or c.e
            }
            for j = 1, #c.items do
                local d = c.items[j]
                if not d.home and not star_blocks_acquisition(snap, anchor, d) then
                    set_item_home(d.item, rid)
                    d.home = rid
                    changed = true
                end
            end
        end
    end
    return changed
end

------------------------------------------------------------
-- Orphan deletion
------------------------------------------------------------

local function delete_confirmed_orphans(snap, active)
    local to_delete = {}
    for i = 1, #snap.regions do
        local rid = snap.regions[i].id
        if active[rid] then
            orphan_seen[rid] = nil
        else
            -- Count at most one observation per cycle: one cycle runs several
            -- passes, and "two settled observations" must mean two cycles.
            local o = orphan_seen[rid]
            if not o then
                o = { count = 0, cycle = -1 }
                orphan_seen[rid] = o
            end
            if o.cycle ~= cycle_id then
                o.count = o.count + 1
                o.cycle = cycle_id
            end
            if o.count >= ORPHAN_CONFIRMATIONS then
                to_delete[#to_delete + 1] = rid
            else
                -- Make sure the confirming observation happens even if the
                -- project does not change again.
                recheck_at = reaper.time_precise() + SETTLE_SEC
            end
        end
    end

    for rid in pairs(orphan_seen) do
        if not snap.region_map[rid] then orphan_seen[rid] = nil end
    end

    if #to_delete == 0 then return false end
    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)
    for i = 1, #to_delete do
        local rid = to_delete[i]
        reaper.DeleteProjectMarker(proj, rid, true)
        orphan_seen[rid] = nil
    end
    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. "remove empty chapter region", -1)
    return true
end

------------------------------------------------------------
-- Region geometry + long-region color
------------------------------------------------------------

local function update_region_geometry_and_color(snap, components, active)
    local changed = false
    reaper.PreventUIRefresh(1)

    for i = 1, #snap.regions do
        local r = snap.regions[i]
        local ci = active[r.id]
        local desired_s, desired_e = r.s, r.e
        if ci then
            local os, oe = owned_span(components[ci], r.id)
            if os then
                desired_s = max(0, os - REGION_PAD)
                desired_e = oe + REGION_PAD
                if abs(desired_s - r.s) > EPS or abs(desired_e - r.e) > EPS then
                    write_entry_by_id(r.id, true, desired_s, desired_e, r.name, r.color)
                    r.s, r.e = desired_s, desired_e
                    changed = true
                end
            end
        end

        local long_now = (desired_e - desired_s) > LONG_REGION_SEC + EPS
        local is_red = r.color == LONG_REGION_RED
        if long_now ~= is_red then
            set_region_default_or_red(r.id, long_now)
            r.color = long_now and LONG_REGION_RED or 0
            changed = true
        end
    end

    reaper.PreventUIRefresh(-1)
    if changed then
        reaper.UpdateTimeline()
        reaper.UpdateArrange()
    end
    return changed
end

local function first_negative_region_requirement(snap, components, active)
    local best = nil
    for rid, ci in pairs(active) do
        local os = owned_span(components[ci], rid)
        if os and os - REGION_PAD < -EPS and (not best or rid < best) then best = rid end
    end
    return best
end

------------------------------------------------------------
-- Project-specific +1h shift
------------------------------------------------------------

local function shift_entire_project_one_hour()
    local getloop = reaper.GetSet_LoopTimeRange2 or function(_, a, b, c, d, e)
        return reaper.GetSet_LoopTimeRange(a, b, c, d, e)
    end
    local cursor = cursor_position()

    local ts_s, ts_e = getloop(proj, false, false, 0, 0, false)
    local lp_s, lp_e = getloop(proj, false, true, 0, 0, false)

    local before = read_snapshot()

    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)

    getloop(proj, true, false, 0, PROJECT_SHIFT_SEC, false)
    if reaper.Main_OnCommandEx then
        reaper.Main_OnCommandEx(40200, 0, proj) -- Insert empty space at time selection
    else
        reaper.Main_OnCommand(40200, 0)
    end

    -- Verify the result. Locked items/markers (or a REAPER preference) may have
    -- kept something in place; shift every such leftover manually so the whole
    -- project really moves as one rigid block.
    local after = read_snapshot()
    for guid, d in pairs(before.item_by_guid) do
        local a = after.item_by_guid[guid]
        if a and abs(a.s - d.s) <= MOVE_EPS then
            reaper.SetMediaItemInfo_Value(a.item, "D_POSITION", d.s + PROJECT_SHIFT_SEC)
        end
    end
    local after_entries = {}
    for i = 1, #after.entries do
        local x = after.entries[i]
        after_entries[(x.isrgn and "R" or "M") .. x.id] = x
    end
    for i = 1, #before.entries do
        local x = before.entries[i]
        local a = after_entries[(x.isrgn and "R" or "M") .. x.id]
        if a and abs(a.s - x.s) <= EPS then
            write_entry_by_id(
                x.id, x.isrgn, x.s + PROJECT_SHIFT_SEC,
                x.isrgn and (x.e + PROJECT_SHIFT_SEC) or 0, x.name, x.color
            )
        end
    end

    if ts_e > ts_s + EPS then
        getloop(proj, true, false, ts_s + PROJECT_SHIFT_SEC, ts_e + PROJECT_SHIFT_SEC, false)
    else
        getloop(proj, true, false, 0, 0, false)
    end
    if lp_e > lp_s + EPS then
        getloop(proj, true, true, lp_s + PROJECT_SHIFT_SEC, lp_e + PROJECT_SHIFT_SEC, false)
    end

    if reaper.SetEditCurPos2 then
        reaper.SetEditCurPos2(proj, cursor + PROJECT_SHIFT_SEC, false, false)
    else
        reaper.SetEditCurPos(cursor + PROJECT_SHIFT_SEC, false, false)
    end

    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. "insert 1 hour at project start", -1)

    -- Our own rigid move is not a user gesture.
    commit_prev_snapshot()
end

-- Offered only when not recording, once per problem and recording session.
local function offer_space_shift(signature)
    if is_recording() then return false end
    signature = signature .. "#rec" .. tostring(record_session)
    if signature == last_space_signature then return false end
    last_space_signature = signature

    local result = reaper.ShowMessageBox(
        "Для сохранения обязательного хвоста 1.5 секунды / 60-секундного разрыва " ..
        "не хватает места перед началом проекта.\n\n" ..
        "Нужно сдвинуть весь проект на 1:00:00 вперед.\n\n" ..
        "OK — вставить один час в начало проекта и продолжить.\n" ..
        "Cancel — ничего пока не менять (предложение повторится после следующей записи).",
        MSG_TITLE,
        1
    )
    if result == 1 then
        shift_entire_project_one_hour()
        last_space_signature = ""
        return true
    end
    return false
end

------------------------------------------------------------
-- Chapter item selection between resolved boundaries
------------------------------------------------------------

local function items_between_boundaries(snap, left_boundary, right_boundary)
    local out = {}
    right_boundary = right_boundary or math.huge
    for i = 1, #snap.items do
        local d = snap.items[i]
        if d.s >= left_boundary - EPS and d.s < right_boundary - EPS then
            out[#out + 1] = d
        end
    end
    sort_items(out)
    return out
end

------------------------------------------------------------
-- Silent internal gap compaction for a chapter being finalized
------------------------------------------------------------

local function compute_compaction(items)
    local comps = build_components(items, PACK_GAP)
    local pos = {}
    local comp_delta = {}
    local cumulative = 0
    local prev_end = nil

    for ci = 1, #comps do
        local c = comps[ci]
        if prev_end then
            local current_start = c.s + cumulative
            local gap = current_start - prev_end
            if gap > PACK_GAP + EPS then
                cumulative = cumulative - (gap - COMPACT_GAP)
            end
        end
        comp_delta[ci] = cumulative
        for j = 1, #c.items do
            local d = c.items[j]
            pos[d.guid] = d.s + cumulative
        end
        prev_end = c.e + cumulative
    end

    local new_s, new_e = nil, nil
    for i = 1, #items do
        local d = items[i]
        local s = pos[d.guid] or d.s
        local e = s + d.len
        if not new_s or s < new_s then new_s = s end
        if not new_e or e > new_e then new_e = e end
    end

    local function delta_for_point(p)
        if #comps == 0 then return 0 end
        local best_ci = 1
        local best_dist = math.huge
        for ci = 1, #comps do
            local c = comps[ci]
            local dist = interval_distance(p, p, c.s, c.e)
            if dist < best_dist - EPS then
                best_dist = dist
                best_ci = ci
            end
        end
        return comp_delta[best_ci] or 0
    end

    return {
        pos = pos,
        s = new_s,
        e = new_e,
        comps = comps,
        delta_for_point = delta_for_point
    }
end

------------------------------------------------------------
-- Reflow planner: place a chapter before the fixed material on its right
------------------------------------------------------------
--
-- spec = {
--   target_items  = items of the chapter being placed (moved as one unit),
--   target_pos    = optional guid -> position after internal compaction,
--   compaction    = optional compute_compaction() result (for markers),
--   right_wall    = items starting at/after this position never move,
--   anchor        = padded start of the fixed chapter on the right,
--   target_rid    = region that will describe the target (or nil),
--   consumed_star = *** marker deleted by this operation (never moved),
-- }
--
-- Every item that starts before right_wall and is not part of the target is
-- "left material". It is grouped into physical packs (<= 10 s) and each pack is
-- moved rigidly, right to left, only as far as needed. Nothing is ever split,
-- nothing is left behind under a moved chapter, and the final layout is checked
-- for overlaps before the plan is accepted.

local function verify_no_new_overlap(items, new_pos)
    local arr = {}
    for i = 1, #items do
        local d = items[i]
        local ns = new_pos[d.guid] or d.s
        arr[i] = { d = d, ns = ns, ne = ns + d.len }
    end
    table.sort(arr, function(a, b)
        if a.ns ~= b.ns then return a.ns < b.ns end
        return a.d.guid < b.d.guid
    end)

    local open = {}
    for i = 1, #arr do
        local x = arr[i]
        local keep = {}
        for j = 1, #open do
            local a = open[j]
            if a.ne > x.ns + EPS then
                keep[#keep + 1] = a
                local now_ov = min(a.ne, x.ne) - max(a.ns, x.ns)
                if now_ov > EPS and interval_overlap(a.d.s, a.d.e, x.d.s, x.d.e) <= EPS then
                    return false, a.d, x.d
                end
            end
        end
        keep[#keep + 1] = x
        open = keep
    end
    return true
end

local function plan_reflow(snap, components, active, spec)
    local in_target = {}
    local ts, te, raw_s, raw_e
    for i = 1, #spec.target_items do
        local d = spec.target_items[i]
        in_target[d.guid] = true
        local s = (spec.target_pos and spec.target_pos[d.guid]) or d.s
        if not ts or s < ts then ts = s end
        if not te or s + d.len > te then te = s + d.len end
        if not raw_s or d.s < raw_s then raw_s = d.s end
        if not raw_e or d.e > raw_e then raw_e = d.e end
    end
    if not ts then return nil, "EMPTY" end

    local target = {
        is_target = true, chapter = true, items = spec.target_items,
        base_s = ts - REGION_PAD, base_e = te + REGION_PAD,
        orig_s = raw_s - REGION_PAD, orig_e = raw_e + REGION_PAD
    }

    -- Left material, grouped into rigid physical packs.
    local left = {}
    for i = 1, #snap.items do
        local d = snap.items[i]
        if not in_target[d.guid] and d.s < spec.right_wall - EPS then left[#left + 1] = d end
    end
    local blocks = build_components(left, PACK_GAP)

    -- A pack is a "chapter" pack when it carries a region's active material.
    local active_owner = {}
    for rid, ci in pairs(active) do
        local c = components[ci]
        for j = 1, #c.items do
            local d = c.items[j]
            if d.home == rid then active_owner[d.guid] = rid end
        end
    end
    for bi = 1, #blocks do
        local b = blocks[bi]
        b.base_s = b.s - REGION_PAD
        b.base_e = b.e + REGION_PAD
        b.orig_s, b.orig_e = b.base_s, b.base_e
        for j = 1, #b.items do
            if active_owner[b.items[j].guid] then b.chapter = true; break end
        end
    end

    local chain = {}
    for bi = 1, #blocks do chain[bi] = blocks[bi] end
    chain[#chain + 1] = target

    -- Right-to-left packing. Each pack moves only as far as needed:
    --   * the target keeps LAYOUT_GAP to the fixed chapter on its right;
    --   * two chapter packs keep LAYOUT_GAP between their regions;
    --   * material physically connected (<= 10 s) to its right neighbour gets
    --     LAYOUT_GAP too, otherwise it would be glued to that chapter;
    --   * any other pair keeps its original distance (already > 10 s), so
    --     region-less material only travels rigidly with its neighbour.
    local wall = spec.anchor
    for i = #chain, 1, -1 do
        local b = chain[i]
        local gap = LAYOUT_GAP
        if i < #chain then
            local r = chain[i + 1]
            local padded_gap = r.base_s - b.base_e
            local connected = padded_gap + 2 * REGION_PAD <= PACK_GAP + EPS
            if not (b.chapter and r.chapter) and not connected then
                gap = min(LAYOUT_GAP, padded_gap)
            end
        end
        local limit_e = wall - gap
        b.delta = (b.base_e > limit_e + EPS) and (limit_e - b.base_e) or 0
        b.new_s = b.base_s + b.delta
        b.new_e = b.base_e + b.delta
        wall = b.new_s
    end

    -- New item positions + validation.
    local guard = recording_guard()
    local new_pos, orig_pos, block_of = {}, {}, {}
    for i = 1, #chain do
        local b = chain[i]
        for j = 1, #b.items do
            local d = b.items[j]
            block_of[d.guid] = b
            local base = d.s
            if b.is_target and spec.target_pos and spec.target_pos[d.guid] then
                base = spec.target_pos[d.guid]
            end
            local np = base + b.delta
            if abs(np - d.s) > MOVE_EPS then
                if np < -EPS then return nil, "NOT_ENOUGH_SPACE" end
                if guard and d.e > guard - EPS then return nil, "RECORDING" end
                new_pos[d.guid] = np
                orig_pos[d.guid] = d.s
            end
        end
        if b.delta < -EPS and b.new_s < -EPS then return nil, "NOT_ENOUGH_SPACE" end
    end

    if not verify_no_new_overlap(snap.items, new_pos) then return nil, "UNSAFE" end

    -- Regions of moved chapter packs travel with their pack.
    local region_moves = {}
    for rid, ci in pairs(active) do
        local r = snap.region_map[rid]
        if r and rid ~= spec.target_rid then
            local c = components[ci]
            local pack, mixed = nil, false
            for j = 1, #c.items do
                local d = c.items[j]
                if d.home == rid then
                    local b = block_of[d.guid]
                    if not b or b.is_target or (pack and pack ~= b) then mixed = true break end
                    pack = b
                end
            end
            if pack and not mixed and abs(pack.delta) > EPS then
                region_moves[#region_moves + 1] = {
                    id = rid, s = max(0, r.s + pack.delta), e = r.e + pack.delta,
                    name = r.name, color = r.color
                }
            end
        end
    end

    -- Ordinary markers inside a moved pack (padded, original geometry) follow it.
    local marker_moves = {}
    for i = 1, #snap.markers do
        local m = snap.markers[i]
        local best, best_dist = nil, math.huge
        for j = 1, #chain do
            local b = chain[j]
            if m.s >= b.orig_s - EPS and m.s <= b.orig_e + EPS then
                local dist = abs(m.s - (b.orig_s + b.orig_e) * 0.5)
                if dist < best_dist then best, best_dist = b, dist end
            end
        end
        if best then
            local delta = best.delta
            if best.is_target and spec.compaction then
                delta = delta + spec.compaction.delta_for_point(m.s)
            end
            if abs(delta) > EPS then
                marker_moves[#marker_moves + 1] = {
                    id = m.id, pos = max(0, m.s + delta), name = m.name, color = m.color
                }
            end
        end
    end

    -- *** markers keep pointing at the same item: a star that resolves to an item
    -- of moving material travels with that item. Stars resolving to fixed
    -- material (the current chapter boundaries) stay exactly where they are.
    local star_moves = {}
    local resolved = star_resolutions(snap)
    for i = 1, #snap.stars do
        local st = snap.stars[i]
        if st.id ~= spec.consumed_star then
            local res = resolved[st.id]
            if res.status == "VALID" and res.starter and new_pos[res.starter.guid] then
                local delta = new_pos[res.starter.guid] - res.starter.s
                star_moves[#star_moves + 1] = {
                    id = st.id, pos = max(0, st.pos + delta), name = st.name, color = st.color
                }
            end
        end
    end

    return {
        target = target,
        chain = chain,
        new_pos = new_pos,
        orig_pos = orig_pos,
        region_moves = region_moves,
        marker_moves = marker_moves,
        star_moves = star_moves
    }
end

-- Apply a plan as one undo point. Items are resolved again by GUID from a fresh
-- read (pointers may change after Undo) and must still be where the plan saw
-- them; otherwise nothing is written.
local function apply_reflow(plan, undo_name, before_moves, after_moves)
    local live = read_snapshot()
    for guid, from in pairs(plan.orig_pos) do
        local d = live.item_by_guid[guid]
        if not d or abs(d.s - from) > MOVE_EPS then return false end
    end

    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)

    local ok = true
    if before_moves then ok = before_moves(live) ~= false end

    if ok then
        for guid, np in pairs(plan.new_pos) do
            reaper.SetMediaItemInfo_Value(live.item_by_guid[guid].item, "D_POSITION", np)
        end
        for i = 1, #plan.region_moves do
            local x = plan.region_moves[i]
            write_entry_by_id(x.id, true, x.s, x.e, x.name, x.color)
        end
        for i = 1, #plan.marker_moves do
            local x = plan.marker_moves[i]
            write_entry_by_id(x.id, false, x.pos, 0, x.name, x.color)
        end
        for i = 1, #plan.star_moves do
            local x = plan.star_moves[i]
            write_entry_by_id(x.id, false, x.pos, 0, x.name, x.color)
        end
        if after_moves then ok = after_moves(live) ~= false end
    end

    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. undo_name, -1)

    -- Our own rigid moves are not user gestures.
    commit_prev_snapshot()
    return ok
end

local function report_unsafe_once(sig)
    if is_recording() or sig == last_unsafe_signature then return end
    last_unsafe_signature = sig
    message(
        "Главу нельзя автоматически разместить без наложения айтемов друг на друга, " ..
        "поэтому скрипт ничего не изменил.\n\n" ..
        "Проверьте монтаж слева от текущей главы (наложения, айтемы между главами) " ..
        "и разведите материал вручную."
    )
end

------------------------------------------------------------
-- Safe one-press Undo rebasing for a pure newly inserted split marker
------------------------------------------------------------

local function geometry_same_except_stars(a, b)
    if not a or not b then return false end
    if #a.items ~= #b.items or #a.regions ~= #b.regions or #a.markers ~= #b.markers then
        return false
    end
    for guid, d in pairs(a.item_by_guid) do
        local o = b.item_by_guid[guid]
        if not o or abs(d.s - o.s) > MOVE_EPS or abs(d.e - o.e) > MOVE_EPS then return false end
    end
    for rid, r in pairs(a.region_map) do
        local o = b.region_map[rid]
        if not o or abs(r.s - o.s) > EPS or abs(r.e - o.e) > EPS then return false end
    end
    local bm = {}
    for i = 1, #b.markers do bm[b.markers[i].id] = b.markers[i] end
    for i = 1, #a.markers do
        local m = a.markers[i]
        local o = bm[m.id]
        if not o or abs(m.s - o.s) > EPS then return false end
    end
    return true
end

local function try_remove_new_star_from_undo(snap, star)
    if is_recording() then return false end
    if not prev_snap or prev_snap.star_map[star.id] then return false end
    if not geometry_same_except_stars(snap, prev_snap) then return false end
    if own_action_on_undo_stack() then return false end

    local ok = reaper.Undo_DoUndo2(proj)
    if ok == 0 then return false end

    local test = read_snapshot()
    local removed = not test.star_map[star.id]
    local geometry_ok = geometry_same_except_stars(test, prev_snap)
    if removed and geometry_ok then
        return true
    end

    -- We undid something broader than a pure marker insertion. Put it back.
    reaper.Undo_DoRedo2(proj)
    return false
end

------------------------------------------------------------
-- Intentional split of one existing region by ***
------------------------------------------------------------

local function find_region_split_by_star(snap, components, active, star)
    local res = resolve_star(snap, star, math.huge)
    if res.status ~= "VALID" then return nil, res end
    local boundary = res.boundary

    local matches = {}
    for rid, ci in pairs(active) do
        local c = components[ci]
        local has_left, has_right = false, false
        for j = 1, #c.items do
            local d = c.items[j]
            if d.home == rid then
                if d.s < boundary - EPS then has_left = true else has_right = true end
            end
        end
        if has_left and has_right then
            matches[#matches + 1] = { rid = rid, ci = ci, c = c, boundary = boundary, res = res }
        end
    end

    if #matches == 1 then return matches[1], res end
    if #matches > 1 then
        return nil, { status = "BLOCK", reason = "MULTI_REGION", star = star }
    end
    return nil, res
end

local function build_split_plan(snap, components, active, split, star)
    local c = split.c
    local target_items, right_items = {}, {}
    for j = 1, #c.items do
        local d = c.items[j]
        if d.s < split.boundary - EPS then
            target_items[#target_items + 1] = d
        else
            right_items[#right_items + 1] = d
        end
    end
    if #target_items == 0 or #right_items == 0 then return nil, "EMPTY" end

    local rs, re = nil, nil
    for i = 1, #right_items do
        local d = right_items[i]
        if not rs or d.s < rs then rs = d.s end
        if not re or d.e > re then re = d.e end
    end

    local plan, reason = plan_reflow(snap, components, active, {
        target_items = target_items,
        right_wall = split.boundary,
        anchor = rs - REGION_PAD,
        target_rid = split.rid,
        consumed_star = star.id
    })
    if not plan then return nil, reason end
    plan.rid = split.rid
    plan.boundary = split.boundary
    plan.right_s = rs - REGION_PAD
    plan.right_e = re + REGION_PAD
    return plan
end

local function apply_existing_region_split(snap, plan, star)
    local rid = plan.rid
    local rebased = try_remove_new_star_from_undo(snap, star)
    local new_rid = nil

    local ok = apply_reflow(plan, "split chapter at ***",
        function()
            -- If the only user action was inserting this fresh ***, it has just
            -- been undone above, so one Ctrl+Z returns to the pre-marker state.
            -- Otherwise the marker is consumed inside this undo block.
            if not rebased then reaper.DeleteProjectMarker(proj, star.id, false) end
            return true
        end,
        function(live)
            local r = live.region_map[rid]
            if not r then return false end
            write_entry_by_id(rid, true, max(0, plan.target.new_s), plan.target.new_e, r.name, r.color)

            new_rid = add_chapter_region(plan.right_s, plan.right_e)
            if not new_rid then return false end

            for i = 1, #live.items do
                local d = live.items[i]
                if d.home == rid then
                    -- live positions are pre-move; right material never moves.
                    if d.s >= plan.boundary - EPS then set_item_home(d.item, new_rid) end
                elseif d.home == new_rid then
                    set_item_home(d.item, nil) -- stale reference to a reused region ID
                end
            end
            return true
        end
    )

    if not ok then
        if rebased then reaper.Undo_DoRedo2(proj) end
        return false
    end

    local_split_pending[star.id] = nil
    orphan_seen[rid] = nil
    if new_rid then orphan_seen[new_rid] = nil end
    return true
end

local function star_inside_existing_region(snap, star)
    for i = 1, #snap.regions do
        local r = snap.regions[i]
        if star.pos > r.s + EPS and star.pos < r.e - EPS then return true end
    end
    return false
end

local function split_block_text(res)
    if res.reason == "OVERLAP" then
        return "под маркером одновременно находится несколько айтемов"
    elseif res.reason == "DEEP_ITEM" then
        return string.format("маркер стоит слишком глубоко внутри айтема (%.1f сек от его начала)", res.offset or 0)
    elseif res.reason == "MULTI_REGION" then
        return "маркер одновременно делит несколько регионов"
    end
    return "граница главы неоднозначна"
end

local function process_new_region_star_splits(snap, components, active, allow_prompts)
    -- Clean pending command IDs that the user removed manually.
    for id in pairs(local_split_pending) do
        if not snap.star_map[id] then local_split_pending[id] = nil end
    end

    local new_ids = new_star_id_set(snap)
    local ordered = stars_in_birth_order(snap)

    for i = 1, #ordered do
        local star = ordered[i]
        if new_ids[star.id] or local_split_pending[star.id] then
            local split, res = find_region_split_by_star(snap, components, active, star)
            if split then
                local_split_pending[star.id] = true
                last_split_block_signature = ""
                local plan, reason = build_split_plan(snap, components, active, split, star)
                if plan then
                    return apply_existing_region_split(snap, plan, star)
                end
                if reason == "NOT_ENOUGH_SPACE" and allow_prompts then
                    local sig = "split@" .. tostring(star.id) .. "@" .. tostring(split.rid)
                    if offer_space_shift(sig) then return true end
                elseif reason == "UNSAFE" and allow_prompts then
                    report_unsafe_once("split@" .. tostring(star.id))
                end
                -- RECORDING / declined space: the command waits; one split per cycle.
                return false
            elseif res and res.status == "BLOCK" and star_inside_existing_region(snap, star) then
                local_split_pending[star.id] = true
                if allow_prompts and not is_recording() then
                    local sig = "localsplitblock@" .. tostring(star.id) .. "@" .. tostring(res.reason)
                    if sig ~= last_split_block_signature then
                        last_split_block_signature = sig
                        message(
                            "Невозможно автоматически разделить главу по ***: " .. split_block_text(res) .. ".\n\n" ..
                            "Разберите монтаж в районе маркера так, чтобы *** стоял в пустоте " ..
                            "или не дальше 15 секунд от начала единственного айтема."
                        )
                    end
                end
                return false
            elseif local_split_pending[star.id] then
                -- The user edited the project so the marker no longer divides an
                -- existing chapter. Release it back to ordinary pending-star logic.
                local_split_pending[star.id] = nil
            end
        end
    end
    return false
end

------------------------------------------------------------
-- Pending A/B(/C) organizer scan
------------------------------------------------------------

local function blocker_text(res, which)
    if not res then return "неизвестная ошибка границы" end
    if res.reason == "OVERLAP" then
        return which .. " *** попал в наложение нескольких айтемов"
    elseif res.reason == "DEEP_ITEM" then
        return string.format(
            "%s *** стоит на %.1f сек от начала айтема (разрешено максимум 15 сек)",
            which, res.offset or 0
        )
    elseif res.reason == "MULTI_REGION" then
        return which .. " *** одновременно делит несколько регионов"
    elseif res.reason == "ORDER" then
        return "второй *** стоит на таймлайне раньше первого"
    elseif res.reason == "ORDER3" then
        return "третий *** стоит на таймлайне раньше второго"
    end
    return which .. " *** имеет неоднозначную границу"
end

local function classify_pending_pair(snap, stars)
    if #stars < 2 then return nil end
    local A, B, C = stars[1], stars[2], stars[3]

    local ar = resolve_star(snap, A, B.pos)
    local br = resolve_star(snap, B, C and C.pos or math.huge)

    if ar.status == "BLOCK" then return { status = "BLOCK", which = "Первый", res = ar, A=A,B=B,C=C } end
    if br.status == "BLOCK" then return { status = "BLOCK", which = "Второй", res = br, A=A,B=B,C=C } end
    if ar.status == "WAIT" then return { status = "WAIT", A=A,B=B,C=C } end
    if br.status == "WAIT" and not C then return { status = "WAIT", A=A,B=B,C=C } end

    -- With C, br can only be VALID (possibly EMPTY) because the finite limit makes
    -- an empty B->C chapter explicit rather than WAITING.
    if br.status ~= "VALID" then return { status = "WAIT", A=A,B=B,C=C } end

    if br.boundary < ar.boundary - EPS then
        -- With only A+B there is no completed B chapter yet. The second marker is
        -- explicitly allowed to move without changing its insertion-order role,
        -- so a temporary spatial crossing is only an unfinished edit. Once C
        -- exists, A->B must be finalized and the crossed boundaries are genuinely
        -- ambiguous, therefore the normal blocker becomes appropriate.
        if not C then return { status = "WAIT", A=A,B=B,C=C, ar=ar, br=br } end
        return { status = "BLOCK", which = "Второй", res = { reason = "ORDER" }, A=A,B=B,C=C }
    end
    if C and C.pos < B.pos - EPS then
        return { status = "BLOCK", which = "Третий", res = { reason = "ORDER3" }, A=A,B=B,C=C }
    end

    local previous = items_between_boundaries(snap, ar.boundary, br.boundary)
    local current_limit = math.huge
    if C then
        local current_right = resolve_star(snap, C, math.huge)
        if current_right.status == "VALID" then
            current_limit = current_right.boundary
        else
            -- We only need B->C for the third-star override; C itself may not yet
            -- have a next item. The marker position is still the upper semantic wall.
            current_limit = C.pos
        end
    end
    local current = items_between_boundaries(snap, br.boundary, current_limit)

    return {
        status = "READY_SCAN",
        A=A, B=B, C=C,
        ar=ar, br=br,
        previous=previous,
        current=current,
        current_audio=occupied_duration(current),
        current_count=#current
    }
end

local function pending_is_mature(scan)
    if scan.C then return true end
    return scan.current_count >= MATURE_MIN_ITEMS
       and scan.current_audio > MATURE_AUDIO_SEC + EPS
end

------------------------------------------------------------
-- Apply finalized A->B chapter reflow
------------------------------------------------------------

local function build_finalize_plan(snap, components, active, scan)
    if #scan.previous == 0 then return nil, "EMPTY" end

    local in_prev = {}
    for i = 1, #scan.previous do in_prev[scan.previous[i].guid] = true end

    -- A->B reuses an existing region only if that region's whole active pack lies
    -- inside A->B. Pieces of chapters whose main pack is elsewhere are simply
    -- transferred to the A->B chapter; their regions are left untouched.
    local candidates = {}
    for rid, ci in pairs(active) do
        local c = components[ci]
        local any, all = false, true
        for j = 1, #c.items do
            local d = c.items[j]
            if d.home == rid then
                if in_prev[d.guid] then any = true else all = false end
            end
        end
        if any and all then candidates[#candidates + 1] = rid end
    end
    table.sort(candidates)
    if #candidates > 1 then return nil, "MULTIPLE_REGIONS" end
    local existing_rid = candidates[1]

    local compaction = compute_compaction(scan.previous)
    if not compaction.s or not compaction.e then return nil, "EMPTY" end

    local anchor
    if #scan.current > 0 then
        local cmin = math.huge
        for i = 1, #scan.current do if scan.current[i].s < cmin then cmin = scan.current[i].s end end
        anchor = cmin - REGION_PAD
    else
        anchor = scan.B.pos
    end

    local plan, reason = plan_reflow(snap, components, active, {
        target_items = scan.previous,
        target_pos = compaction.pos,
        compaction = compaction,
        right_wall = scan.br.boundary,
        anchor = anchor,
        target_rid = existing_rid,
        consumed_star = scan.A.id
    })
    if not plan then return nil, reason end
    plan.existing_rid = existing_rid
    plan.scan = scan
    return plan
end

local function apply_finalize_plan(plan)
    local scan = plan.scan
    local target_set = {}
    for i = 1, #scan.previous do target_set[scan.previous[i].guid] = true end
    local final_rid = nil

    local ok = apply_reflow(plan, "finalize chapter and maintain 60s layout", nil, function(live)
        local rid = plan.existing_rid
        local s, e = max(0, plan.target.new_s), plan.target.new_e
        local r = rid and live.region_map[rid] or nil
        if r then
            write_entry_by_id(rid, true, s, e, r.name, r.color)
        else
            rid = add_chapter_region(s, e)
            if not rid then return false end
        end
        final_rid = rid

        -- A live *** boundary is authoritative for FINALIZATION: the region owns
        -- exactly the A->B items. Any other item still pointing at this region ID
        -- (outside the chapter, or a stale reference to a reused ID) forgets it.
        for i = 1, #live.items do
            local d = live.items[i]
            if target_set[d.guid] then
                set_item_home(d.item, rid)
            elseif d.home == rid then
                set_item_home(d.item, nil)
            end
        end

        -- A is consumed only after the region definitely exists.
        reaper.DeleteProjectMarker(proj, scan.A.id, false)
        return true
    end)

    if final_rid then orphan_seen[final_rid] = nil end
    return ok
end

local function handle_empty_pair(scan, allow_prompts)
    if is_recording() or not allow_prompts then return false end
    local sig = string.format("empty@%d@%.6f@%d@%.6f", scan.A.id, scan.A.pos, scan.B.id, scan.B.pos)
    if sig == last_empty_signature then return false end
    last_empty_signature = sig

    message(
        "Между первым и вторым *** нет айтемов.\n\n" ..
        "Первый *** будет удалён, второй останется началом текущей главы."
    )
    -- The modal dialog may have taken a while: delete A only if it is still there.
    local fresh = read_snapshot()
    if not fresh.star_map[scan.A.id] then return false end
    reaper.Undo_BeginBlock2(proj)
    reaper.DeleteProjectMarker(proj, scan.A.id, false)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. "remove empty chapter marker", -1)
    return true
end

-- One organizer step. Returns true when the project was changed (the caller then
-- runs another follower pass and calls this again).
local function organizer_step(allow_prompts)
    local snap = read_snapshot()
    local stars = pending_stars(snap)
    if #stars < 2 then
        last_blocker_signature = ""
        last_empty_signature = ""
        return false
    end

    local scan = classify_pending_pair(snap, stars)
    if not scan then return false end

    if scan.status == "BLOCK" then
        if allow_prompts and not is_recording() then
            local sig = string.format(
                "block@%d@%.6f@%d@%.6f@%s",
                scan.A.id, scan.A.pos, scan.B.id, scan.B.pos,
                tostring(scan.res and scan.res.reason)
            )
            if sig ~= last_blocker_signature then
                last_blocker_signature = sig
                message(
                    "Автоматическое создание главы остановлено: " ..
                    blocker_text(scan.res, scan.which) .. ".\n\n" ..
                    "Разберите монтаж у этого маркера. Нормальный вариант — *** в пустоте; " ..
                    "допустимый вариант — внутри единственного айтема, но не дальше 15 секунд от его начала."
                )
            end
        end
        return false
    end

    last_blocker_signature = ""
    if scan.status == "WAIT" then return false end

    if #scan.previous == 0 then
        return handle_empty_pair(scan, allow_prompts)
    end

    if not pending_is_mature(scan) then return false end

    local components = build_components(snap.items, PACK_GAP)
    local active = choose_active_components(snap, components)

    local plan, reason = build_finalize_plan(snap, components, active, scan)
    if not plan then
        if reason == "EMPTY" then
            return handle_empty_pair(scan, allow_prompts)
        elseif reason == "NOT_ENOUGH_SPACE" then
            if allow_prompts then
                local sig = string.format("finalspace@%d@%d", scan.A.id, scan.B.id)
                return offer_space_shift(sig)
            end
        elseif reason == "MULTIPLE_REGIONS" then
            if allow_prompts and not is_recording() then
                local sig = string.format("multirgn@%d@%d", scan.A.id, scan.B.id)
                if sig ~= last_multi_signature then
                    last_multi_signature = sig
                    message(
                        "Между первым и вторым *** уже находятся айтемы нескольких разных регионов-глав.\n\n" ..
                        "Автоматически угадывать, какой регион уничтожать или объединять, скрипт не будет."
                    )
                end
            end
        elseif reason == "UNSAFE" then
            if allow_prompts then
                report_unsafe_once(string.format("final@%d@%d", scan.A.id, scan.B.id))
            end
        end
        -- RECORDING: the reflow would touch material at/after the recording
        -- position; it simply waits for the recording to stop.
        return false
    end

    last_space_signature = ""
    last_multi_signature = ""
    return apply_finalize_plan(plan)
end

------------------------------------------------------------
-- 40 / 50 minute recording helper markers
------------------------------------------------------------

local function find_named_marker_near(snap, name, target)
    local best, best_dist = nil, math.huge
    for i = 1, #snap.markers do
        local m = snap.markers[i]
        if m.name == name then
            local dist = abs(m.s - target)
            if dist <= HELPER_MATCH_SEC + EPS and dist < best_dist then
                best, best_dist = m, dist
            end
        end
    end
    return best
end

local function update_duration_helper_markers()
    local snap = read_snapshot()
    local stars = pending_stars(snap)
    if #stars == 0 then return false end

    local star = stars[#stars]
    local recording = is_recording()
    local res = resolve_star(snap, star, math.huge)

    local chapter_start = nil
    if res.status == "VALID" and res.starter then
        chapter_start = res.boundary
    elseif res.status == "WAIT" and recording and rec_start_pos
        and rec_start_pos >= star.pos - EPS then
        -- First take of a brand-new chapter: its first item will start where the
        -- running recording started.
        chapter_start = rec_start_pos
    end
    if not chapter_start then return false end

    local latest_end = nil
    for i = 1, #snap.items do
        local d = snap.items[i]
        if d.s >= chapter_start - EPS then
            if not latest_end or d.e > latest_end then latest_end = d.e end
        end
    end
    -- While recording, the take in progress is not an item yet: the live play
    -- position tells how far the chapter has been recorded.
    if recording then
        local pp = play_position()
        if pp >= chapter_start - EPS and (not latest_end or pp > latest_end) then latest_end = pp end
    end
    if not latest_end then return false end

    local specs = {
        { name = HELPER_40_NAME, offs = HELPER_40_SEC },
        { name = HELPER_50_NAME, offs = HELPER_50_SEC },
    }
    local actions = {}
    for i = 1, #specs do
        local target = chapter_start + specs[i].offs
        if latest_end >= target - EPS then
            local existing = find_named_marker_near(snap, specs[i].name, target)
            if existing then
                if abs(existing.s - target) > EPS then
                    actions[#actions + 1] = { kind="MOVE", marker=existing, target=target }
                end
            else
                actions[#actions + 1] = { kind="ADD", name=specs[i].name, target=target }
            end
        end
    end

    if #actions == 0 then return false end
    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)
    for i = 1, #actions do
        local a = actions[i]
        if a.kind == "MOVE" then
            write_entry_by_id(a.marker.id, false, a.target, 0, a.marker.name, a.marker.color)
        else
            reaper.AddProjectMarker2(proj, false, a.target, 0, a.name, -1, 0)
        end
    end
    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, UNDO_TAG .. "update 40/50 minute helpers", -1)
    return true
end

------------------------------------------------------------
-- One follower pass
------------------------------------------------------------

-- Returns true when the project was changed structurally and the caller should
-- start a new pass from a fresh snapshot.
local function follower_pass(allow_prompts)
    local snap = read_snapshot()

    -- The hard cap still wins: a fourth insertion is rejected as the newest marker
    -- before it can act as a split command. With one/two/three stars, however, a
    -- fresh local split gets first refusal and cannot consume unrelated markers.
    if enforce_star_capacity(snap) then return true end

    local early_components = build_components(snap.items, PACK_GAP)
    local early_active = choose_active_components(snap, early_components)
    if process_new_region_star_splits(snap, early_components, early_active, allow_prompts) then
        return true
    end

    if sanitize_stars(snap) then return true end
    if clear_stale_homes(snap) then
        snap = read_snapshot()
    end

    local components = build_components(snap.items, PACK_GAP)
    if bootstrap_unowned_regions(snap, components) then
        snap = read_snapshot()
        components = build_components(snap.items, PACK_GAP)
    end

    local active = choose_active_components(snap, components)

    -- A manually drawn region around one detached old-owned pack is an explicit
    -- ownership override. Re-read immediately so the new chapter participates in
    -- collision checks, acquisition, orphan protection, and geometry this cycle.
    if adopt_manually_wrapped_detached_components(snap, components, active) then
        snap = read_snapshot()
        components = build_components(snap.items, PACK_GAP)
        active = choose_active_components(snap, components)
    end

    -- A detached piece that has joined exactly one other active chapter is a
    -- transfer, not a merge. This also repairs the same persisted state after a
    -- script/REAPER restart. Real collisions fall through to the dialog below.
    if transfer_detached_items_between_chapters(snap, components, active) then
        snap = read_snapshot()
        components = build_components(snap.items, PACK_GAP)
        active = choose_active_components(snap, components)
    end

    -- Collision prompt/rollback happens before acquisition or geometry writes.
    if handle_collisions(snap, components, allow_prompts) then return true end

    if acquire_unowned_items(snap, components, active) then
        snap = read_snapshot()
        components = build_components(snap.items, PACK_GAP)
        active = choose_active_components(snap, components)
    end

    -- A chapter that starts closer than 1.5 s to 0:00 cannot have its full pad.
    -- Offer the +1 h shift (not blocking anything); meanwhile the region is
    -- clamped at 0:00.
    local neg_rid = first_negative_region_requirement(snap, components, active)
    if neg_rid and allow_prompts and not is_recording() then
        if offer_space_shift("followerspace@" .. tostring(neg_rid)) then return true end
    end

    if not is_recording() then
        if delete_confirmed_orphans(snap, active) then return true end
    end

    update_region_geometry_and_color(snap, components, active)
    return false
end

------------------------------------------------------------
-- Monolithic cycle
------------------------------------------------------------

local function run_cycle(allow_prompts)
    cycle_id = cycle_id + 1
    -- 1) local split/follower/collision state first;
    -- 2) then insertion-ordered A/B/C organizer (one chapter per pass);
    -- 3) finally helper markers once everything has settled.
    -- The pass limit guarantees termination even if REAPER refuses an edit.
    for _ = 1, MAX_PASSES_PER_CYCLE do
        if not follower_pass(allow_prompts) then
            if not organizer_step(allow_prompts) then
                update_duration_helper_markers()
                return
            end
        end
    end
end

------------------------------------------------------------
-- Initialize / project-tab reinitialize
------------------------------------------------------------

local _, last_tab_signature = get_project_context()

local function reinitialize_current_project(now)
    -- All volatile state belongs to the previously active project and must not
    -- leak into the newly active one. Rebuild exactly as on a fresh script start.
    prev_snap = nil
    orphan_seen = {}

    star_birth_seq = {}
    star_birth_counter = 0
    star_last_pos = {}
    star_tomb = {}
    local_split_pending = {}

    prompted_collisions = {}
    last_blocker_signature = ""
    last_split_block_signature = ""
    last_space_signature = ""
    last_empty_signature = ""
    last_multi_signature = ""
    last_unsafe_signature = ""

    mouse_was_down = false
    last_mouse_release = -math.huge
    recheck_at = nil

    was_recording = is_recording()
    rec_start_pos = was_recording and observe_recording_start() or nil

    -- First pass seeds persistent ownership from existing regions before any
    -- orphan can be deleted. Do not fight an Undo the user has just made.
    if not own_action_on_redo_stack() then
        run_cycle(not was_recording)
    end
    commit_prev_snapshot()

    last_state = reaper.GetProjectStateChangeCount(proj)
    observed_state = last_state
    state_changed_at = now
    last_poll = now
    last_record_scan = now
    last_star_scan = now
    last_helper_scan = now
end

reinitialize_current_project(reaper.time_precise())

------------------------------------------------------------
-- Defer loop
------------------------------------------------------------

local function loop()
    if reaper.GetExtState(SYNC_SECTION, OWNER_KEY) ~= INSTANCE_TOKEN then return end

    local now = reaper.time_precise()
    if now - last_poll < POLL_INTERVAL then
        reaper.defer(loop)
        return
    end
    last_poll = now

    -- Refresh project identity before touching any project API. Closing the tab
    -- that proj used to point at invalidates that ReaProject*, so this check must
    -- happen before GetProjectStateChangeCount(), transport queries, snapshots, etc.
    local active_proj, tab_signature = get_project_context()
    if not active_proj then
        reaper.defer(loop)
        return
    end

    if active_proj ~= proj or tab_signature ~= last_tab_signature then
        proj = active_proj
        last_tab_signature = tab_signature
        evict_project_scoped_instances()
        reinitialize_current_project(now)
        reaper.defer(loop)
        return
    end

    if reaper.GetExtState(SYNC_SECTION, LEGACY_OWNER_KEY) ~= LEGACY_EVICT_TOKEN then
        reaper.SetExtState(SYNC_SECTION, LEGACY_OWNER_KEY, LEGACY_EVICT_TOKEN, false)
    end

    -- Recording start/stop bookkeeping.
    local recording = is_recording()
    if recording and not was_recording then
        rec_start_pos = observe_recording_start()
    elseif not recording and was_recording then
        rec_start_pos = nil
        record_session = record_session + 1
        -- Re-check shortly after the stop even if nothing else changes (for
        -- example when the take was discarded), but not instantly, so our
        -- dialogs never race REAPER's own post-recording dialog.
        recheck_at = now + 1.0
    end
    was_recording = recording

    local state = reaper.GetProjectStateChangeCount(proj)
    if state ~= observed_state then
        observed_state = state
        state_changed_at = now
    end

    if not mouse_gesture_active(now) then
        local star_count = prev_snap and #prev_snap.stars or 0

        local recording_due = recording and star_count >= 2
            and (now - last_record_scan >= RECORD_SCAN_SEC)
        local star_due = star_count >= 2
            and (now - last_star_scan >= STAR_SCAN_SEC)
        local helper_due = recording and star_count >= 1
            and (now - last_helper_scan >= HELPER_SCAN_SEC)
        local settled_change = state ~= last_state and (now - state_changed_at >= SETTLE_SEC)
        local recheck_due = recheck_at ~= nil and now >= recheck_at

        if recording_due or star_due or helper_due or settled_change or recheck_due then
            recheck_at = nil
            if own_action_on_redo_stack() then
                -- The user has just undone one of our edits. Do not fight the
                -- Undo: only observe until the user makes the next change.
                commit_prev_snapshot()
            else
                run_cycle(not recording)
                commit_prev_snapshot()
            end

            last_state = reaper.GetProjectStateChangeCount(proj)
            observed_state = last_state
            state_changed_at = now
            if recording_due then last_record_scan = now end
            if star_due or (prev_snap and #prev_snap.stars >= 2) then last_star_scan = now end
            if helper_due then last_helper_scan = now end
        end
    end

    reaper.defer(loop)
end

loop()
