--[[
  AST Auto Chapter Follow — FINAL v2.4
  ------------------------------------
  Deterministic chapter follower + *** organizer.

  Core rules:
    * every REAPER region is a chapter;
    * chapter region = active owned item pack + 1.5 s on both sides;
    * all project items participate; overlaps are one occupied timeline entity;
    * gap <= 10 s keeps/rejoins a pack; gap > 10 s splits it;
    * when one old chapter is split by a manual item move, the old region stays
      with stationary material; if everything moved, it stays with the earliest
      resulting old-owned pack;
    * detached old-owned items remember their chapter and rejoin if they come back;
    * when a detached part of one chapter is moved into another existing chapter,
      only that part changes ownership: the source region shrinks and the target
      region expands; an ambiguous whole-chapter collision still asks to merge;
    * a manual region drawn around exactly one detached old-owned pack adopts that
      pack as a new chapter, including after script/REAPER restart;
    * a truly empty/orphan region is deleted after two settled observations;
    * regions longer than 59:45 are red; when shorter they return to default color;

  *** rules:
    * markers < 5 s apart: delete the later one silently;
    * pending *** markers are ordered by insertion chronology, not timeline position;
    * at most 3 pending *** markers; newest insertions beyond 3 are deleted;
    * marker in silence -> boundary is the next item start;
    * marker inside exactly one item and <= 15 s from that item's start -> the
      whole item belongs to the new chapter;
    * marker deeper than 15 s into an item, or inside 2+ simultaneous items -> BLOCK;
    * with A+B, finalize A->B once B chapter has >=3 items and >45 s occupied audio;
    * with A+B+C, finalize A->B immediately (maturity override);
    * while only A+B exist, moving the second marker before the first resolved
      boundary is a harmless pending edit: wait silently until C makes the order
      actionable instead of warning before the new chapter has been recorded;
    * if A->B has no items, after recording warn and delete A;
    * hard gaps >10 s inside the chapter being finalized are silently reduced to 9 s;
    * finalized chapters are packed with >=60 s between regions;
    * if there is no room before 0:00, never interrupt recording; after recording
      offer to insert +1 hour at project start, then rescan and continue;
    * a newly inserted valid *** that divides owned material of one existing region
      is a one-shot split command: old region stays left, a new "rename" region is
      created on the right, a 60 s chapter gap is established, and that triggering
      *** is consumed immediately without touching other *** markers;
    * for the current chapter, helper markers "40 min" and "50 min" are created
      at +40:00 and +50:00 from its resolved first item once those times are reached.

  Persistence:
    * each item stores its historical chapter region ID in P_EXT:AST_CHAPTER_REGION.
      This survives script/REAPER restarts and makes large rigid moves deterministic.

  Project tabs:
    * one global watcher follows the active project tab;
    * switching tabs, opening a tab, or closing a tab fully reinitializes volatile
      runtime state from the currently active project.

  Notes:
    * no automation/envelope handling by design;
    * ordinary project markers move with rigid chapter packages;
    * *** markers are semantic boundaries and are never moved by chapter reflow.
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

local EPS                   = 0.0005
local MOVE_EPS              = 0.002

local ITEM_EXT_KEY          = "P_EXT:AST_CHAPTER_REGION"

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
    "final-v2.3@%.9f@%s",
    reaper.time_precise(),
    tostring({}):gsub("table: ", "")
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
    set_toggle(0)
    if reaper.GetExtState(SYNC_SECTION, OWNER_KEY) == INSTANCE_TOKEN then
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
local orphan_seen = {}

-- Marker insertion chronology is runtime state. Existing markers found on script
-- startup are seeded in timeline order; every marker observed later is appended.
-- This is deliberately separate from snap.stars, which remains timeline-sorted
-- for spatial calculations.
local star_birth_seq = {}
local star_birth_counter = 0
local local_split_pending = {}

local last_state = reaper.GetProjectStateChangeCount(proj)
local observed_state = last_state
local state_changed_at = reaper.time_precise()
local last_poll = 0
local last_record_scan = -math.huge
local last_star_scan = -math.huge
local last_helper_scan = -math.huge

local last_collision_signature = ""
local last_blocker_signature = ""
local last_space_signature = ""
local last_empty_signature = ""

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
    table.sort(ids)
    local out = {}
    for i = 1, #ids do out[i] = tostring(ids[i]) end
    return table.concat(out, ",")
end

------------------------------------------------------------
-- Project marker/region helpers
------------------------------------------------------------

local function find_entry_by_id(id, want_region)
    local total = reaper.GetNumRegionsOrMarkers
        and reaper.GetNumRegionsOrMarkers(proj)
        or select(1, reaper.CountProjectMarkers(proj))

    if not reaper.GetNumRegionsOrMarkers then
        local _, nm, nr = reaper.CountProjectMarkers(proj)
        total = nm + nr
    end

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
        id, name or "", color or x.color or 0, 0
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

------------------------------------------------------------
-- *** insertion chronology
------------------------------------------------------------

local function sync_star_birth_order(snap)
    local present = {}
    for i = 1, #snap.stars do present[snap.stars[i].id] = snap.stars[i] end

    -- Forget markers that no longer exist so a future reused marker ID is fresh.
    for id in pairs(star_birth_seq) do
        if not present[id] then star_birth_seq[id] = nil end
    end

    local newcomers = {}
    for i = 1, #snap.stars do
        local st = snap.stars[i]
        if not star_birth_seq[st.id] then newcomers[#newcomers + 1] = st end
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

    snap.stars_by_birth = {}
    for i = 1, #snap.stars do snap.stars_by_birth[i] = snap.stars[i] end
    table.sort(snap.stars_by_birth, function(a, b)
        local sa = star_birth_seq[a.id] or math.huge
        local sb = star_birth_seq[b.id] or math.huge
        if sa == sb then return a.id < b.id end
        return sa < sb
    end)
end

local function stars_in_birth_order(snap)
    return snap.stars_by_birth or snap.stars
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
            elseif name == STAR_NAME then
                local st = { id = id, pos = s, color = color, enum_idx = i }
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
                    orphan_seen[r.id] = 0
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
        orphan_seen[p.target] = 0
        orphan_seen[p.source] = 0
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
        orphan_seen[p.target] = 0
        for rid in pairs(p.sources) do orphan_seen[rid] = 0 end
    end
    reaper.PreventUIRefresh(-1)
    last_collision_signature = ""
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

local function star_blocks_acquisition(snap, active_component, item)
    for i = 1, #snap.stars do
        local st = snap.stars[i]
        local res = resolve_star(snap, st, math.huge)
        local boundary = nil
        if res.status == "VALID" then boundary = res.boundary end

        if boundary then
            if active_component.e <= boundary + EPS and item.s >= boundary - EPS then
                return true
            end
            if active_component.s >= boundary - EPS and item.e <= boundary + EPS then
                return true
            end
        elseif res.status == "BLOCK" then
            -- Conservative around an ambiguous live boundary: never acquire fresh
            -- material through it. Historical ownership is intentionally preserved.
            if active_component.e <= st.pos + EPS and item.e > st.pos + EPS then
                return true
            end
            if active_component.s >= st.pos - EPS and item.s < st.pos - EPS then
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

local function enforce_star_capacity(snap)
    if #snap.stars <= 3 then return false end

    local by_birth = {}
    for i = 1, #snap.stars do by_birth[i] = snap.stars[i] end
    table.sort(by_birth, function(a, b)
        local sa = star_birth_seq[a.id] or math.huge
        local sb = star_birth_seq[b.id] or math.huge
        if sa == sb then return a.id < b.id end
        return sa < sb
    end)

    local to_delete = {}
    for i = #by_birth, 4, -1 do
        to_delete[#to_delete + 1] = by_birth[i].id
    end
    if #to_delete == 0 then return false end

    reaper.PreventUIRefresh(1)
    for i = 1, #to_delete do
        reaper.DeleteProjectMarker(proj, to_delete[i], false)
        local_split_pending[to_delete[i]] = nil
    end
    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    return true
end

local function sanitize_stars(snap)
    if #snap.stars == 0 then return false end
    local to_delete = {}
    local kept = {}

    for i = 1, #snap.stars do
        local st = snap.stars[i]
        local prev = kept[#kept]
        if prev and (st.pos - prev.pos) < STAR_DUPLICATE_SEC - EPS then
            to_delete[st.id] = true -- later chronologically
        else
            kept[#kept + 1] = st
        end
    end

    local surviving = {}
    for i = 1, #snap.stars do
        if not to_delete[snap.stars[i].id] then surviving[#surviving + 1] = snap.stars[i] end
    end

    if #surviving > 3 then
        local excess = #surviving - 3
        local by_birth = {}
        for i = 1, #surviving do by_birth[i] = surviving[i] end
        table.sort(by_birth, function(a, b)
            local sa = star_birth_seq[a.id] or math.huge
            local sb = star_birth_seq[b.id] or math.huge
            if sa == sb then return a.id < b.id end
            return sa < sb
        end)

        -- Capacity is about insertion chronology, not timeline chronology. Preserve
        -- the three oldest pending markers and reject every newest excess marker.
        for i = #by_birth, 1, -1 do
            if excess <= 0 then break end
            local st = by_birth[i]
            if not to_delete[st.id] then
                to_delete[st.id] = true
                excess = excess - 1
            end
        end
    end

    if table_count(to_delete) == 0 then return false end

    reaper.PreventUIRefresh(1)
    for id in pairs(to_delete) do
        reaper.DeleteProjectMarker(proj, id, false)
    end
    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    return true
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

    -- Prompt only when at least one pair of the same chapter-owned material was
    -- genuinely farther than 10 s before this user action and is connected now.
    -- A rigid move of the whole project preserves all previous distances and must
    -- never be interpreted as a merge gesture.
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

local function collision_candidates(snap, components)
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
        if #rids >= 2 and any_moved then
            table.sort(rids)
            if collision_is_new_proximity(c, rids) then
                out[#out + 1] = { ci = ci, c = c, stats = st, rids = rids }
            end
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
        if d.home == survivor or losing[d.home] then
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
    reaper.Undo_EndBlock2(proj, "Merge chapter regions", -1)
    return true
end

local function handle_collisions(snap, components)
    local list = collision_candidates(snap, components)
    if #list == 0 then
        last_collision_signature = ""
        return false, false
    end

    if is_recording() then return false, true end

    local col = list[1]
    local sig = ids_signature(col.rids) .. string.format("@%.6f@%.6f", col.c.s, col.c.e)
    if sig == last_collision_signature then return false, true end
    last_collision_signature = sig

    local result = reaper.ShowMessageBox(
        "Айтемы двух глав оказались ближе 10 секунд и теперь образуют один физический пакет.\n\n" ..
        "Объединить главы?\n\n" ..
        "Да — объединить главы и удалить старый лишний регион.\n" ..
        "Нет — отменить последнее пользовательское перемещение.",
        "Chapter automation",
        4
    )

    if result == 6 then -- Yes
        merge_collision(snap, col)
    else
        rollback_user_move()
    end

    return true, true
end

------------------------------------------------------------
-- Acquisition of fresh/unowned items into one existing chapter
------------------------------------------------------------

local function acquire_unowned_items(snap, components, active)
    local changed = false
    local active_ci_to_rid = {}
    for rid, ci in pairs(active) do
        if not active_ci_to_rid[ci] then
            active_ci_to_rid[ci] = rid
        else
            active_ci_to_rid[ci] = false -- collision; handled elsewhere
        end
    end

    for ci = 1, #components do
        local rid = active_ci_to_rid[ci]
        if rid then
            local c = components[ci]
            local owned_s, owned_e = nil, nil
            for j = 1, #c.items do
                local d = c.items[j]
                if d.home == rid then
                    if not owned_s or d.s < owned_s then owned_s = d.s end
                    if not owned_e or d.e > owned_e then owned_e = d.e end
                end
            end
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
            orphan_seen[rid] = 0
        else
            orphan_seen[rid] = (orphan_seen[rid] or 0) + 1
            if orphan_seen[rid] >= ORPHAN_CONFIRMATIONS then
                to_delete[#to_delete + 1] = rid
            end
        end
    end

    for rid in pairs(orphan_seen) do
        if not snap.region_map[rid] then orphan_seen[rid] = nil end
    end

    if #to_delete == 0 then return false end
    reaper.PreventUIRefresh(1)
    for i = 1, #to_delete do
        local rid = to_delete[i]
        reaper.DeleteProjectMarker(proj, rid, true)
        orphan_seen[rid] = nil
    end
    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
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
        if ci then
            local c = components[ci]
            local desired_s = c.s - REGION_PAD
            local desired_e = c.e + REGION_PAD

            if desired_s >= -EPS then
                if desired_s < 0 then desired_s = 0 end
                if abs(desired_s - r.s) > EPS or abs(desired_e - r.e) > EPS then
                    write_entry_by_id(r.id, true, desired_s, desired_e, r.name, r.color)
                    r.s, r.e = desired_s, desired_e
                    changed = true
                end
            end

            local long_now = (desired_e - desired_s) > LONG_REGION_SEC + EPS
            local is_red = r.color == LONG_REGION_RED
            if long_now ~= is_red then
                set_region_default_or_red(r.id, long_now)
                r.color = long_now and LONG_REGION_RED or 0
                changed = true
            end
        else
            local duration = r.e - r.s
            local long_now = duration > LONG_REGION_SEC + EPS
            local is_red = r.color == LONG_REGION_RED
            if long_now ~= is_red then
                set_region_default_or_red(r.id, long_now)
                r.color = long_now and LONG_REGION_RED or 0
                changed = true
            end
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
    for rid, ci in pairs(active) do
        local c = components[ci]
        if c.s - REGION_PAD < -EPS then return rid end
    end
    return nil
end

------------------------------------------------------------
-- Project-specific +1h shift
------------------------------------------------------------

local function shift_entire_project_one_hour()
    local getloop = reaper.GetSet_LoopTimeRange2 or function(_, a,b,c,d,e)
        return reaper.GetSet_LoopTimeRange(a,b,c,d,e)
    end
    local cursor = reaper.GetCursorPositionEx and reaper.GetCursorPositionEx(proj)
        or reaper.GetCursorPosition()

    local ts_s, ts_e = getloop(proj, false, false, 0, 0, false)
    local lp_s, lp_e = getloop(proj, false, true, 0, 0, false)

    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)

    getloop(proj, true, false, 0, PROJECT_SHIFT_SEC, false)
    if reaper.Main_OnCommandEx then
        reaper.Main_OnCommandEx(40200, 0, proj)
    else
        reaper.Main_OnCommand(40200, 0)
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
    reaper.Undo_EndBlock2(proj, "Insert 1 hour at project start", -1)
end

local function offer_space_shift(signature)
    if is_recording() then return false end
    if signature == last_space_signature then return false end
    last_space_signature = signature

    local result = reaper.ShowMessageBox(
        "Для сохранения обязательного хвоста 1.5 секунды / 60-секундного разрыва " ..
        "не хватает места перед началом проекта.\n\n" ..
        "Нужно сдвинуть весь проект на 1:00:00 вперед.\n\n" ..
        "OK — вставить один час в начало проекта и продолжить.\n" ..
        "Cancel — ничего пока не менять.",
        "Chapter automation",
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

local function region_ids_in_items(snap, items)
    local set = {}
    for i = 1, #items do
        local rid = items[i].home
        if rid and snap.region_map[rid] then set[rid] = true end
    end
    local out = {}
    for rid in pairs(set) do out[#out + 1] = rid end
    table.sort(out)
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
-- Build active chapter packages from regions
------------------------------------------------------------

local function active_region_packages(snap, components, active)
    local out = {}
    for i = 1, #snap.regions do
        local r = snap.regions[i]
        local ci = active[r.id]
        if ci then
            local c = components[ci]
            out[#out + 1] = {
                rid = r.id,
                region = r,
                items = c.items,
                base_s = c.s - REGION_PAD,
                base_e = c.e + REGION_PAD,
                delta = 0
            }
        end
    end
    table.sort(out, function(a, b)
        if abs(a.base_s - b.base_s) <= EPS then return a.rid < b.rid end
        return a.base_s < b.base_s
    end)
    return out
end

local function chapter_for_marker_point(p, packages)
    local best, best_dist = nil, math.huge
    for i = 1, #packages do
        local ch = packages[i]
        if p >= ch.base_s - EPS and p <= ch.base_e + EPS then
            local center = (ch.base_s + ch.base_e) * 0.5
            local dist = abs(p - center)
            if dist < best_dist then best, best_dist = ch, dist end
        end
    end
    return best
end

------------------------------------------------------------
-- Generic right-to-left packing before an anchor
------------------------------------------------------------

local function plan_pack_before_anchor(packages, target, anchor_start)
    local chapters = {}
    for i = 1, #packages do
        local ch = packages[i]
        if ch.rid ~= target.rid and ch.base_s < anchor_start - EPS then
            chapters[#chapters + 1] = ch
        end
    end
    chapters[#chapters + 1] = target

    table.sort(chapters, function(a, b)
        if abs(a.base_s - b.base_s) <= EPS then
            if a.is_target ~= b.is_target then return not a.is_target end
            return (a.rid or math.huge) < (b.rid or math.huge)
        end
        return a.base_s < b.base_s
    end)

    if chapters[#chapters] ~= target then
        return nil, "OTHER_REGION_AFTER_TARGET"
    end

    local anchor = anchor_start
    for i = #chapters, 1, -1 do
        local ch = chapters[i]
        local desired_end = anchor - LAYOUT_GAP
        ch.delta = ch.base_e > desired_end + EPS and (desired_end - ch.base_e) or 0
        ch.new_s = ch.base_s + ch.delta
        ch.new_e = ch.base_e + ch.delta
        anchor = ch.new_s
    end

    if chapters[1] and chapters[1].new_s < -EPS then
        return nil, "NOT_ENOUGH_SPACE"
    end
    return chapters
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
    if not prev_snap or prev_snap.star_map[star.id] then return false end
    if not geometry_same_except_stars(snap, prev_snap) then return false end

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
    local limit = math.huge
    local res = resolve_star(snap, star, limit)
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

local function apply_existing_region_split(snap, components, active, split, star)
    local rid = split.rid
    local old_region = snap.region_map[rid]
    if not old_region then return false end

    local left, right = {}, {}
    for i = 1, #snap.items do
        local d = snap.items[i]
        if d.home == rid then
            if d.s < split.boundary - EPS then left[#left + 1] = d else right[#right + 1] = d end
        end
    end
    if #left == 0 or #right == 0 then return false end
    sort_items(left); sort_items(right)

    local left_comps = build_components(left, PACK_GAP)
    local right_comps = build_components(right, PACK_GAP)
    -- Existing active chapter was connected. If historical detached pieces exist,
    -- only the earliest resulting pack is the physical chapter on each side.
    local left_active = left_comps[#left_comps] or nil
    local right_active = right_comps[1] or nil
    if not left_active or not right_active then return false end

    local target = {
        rid = rid,
        region = old_region,
        items = left_active.items,
        base_s = left_active.s - REGION_PAD,
        base_e = left_active.e + REGION_PAD,
        is_target = true,
        delta = 0
    }
    local right_s = right_active.s - REGION_PAD
    local right_e = right_active.e + REGION_PAD

    local packages = active_region_packages(snap, components, active)
    local plan, reason = plan_pack_before_anchor(packages, target, right_s)
    if not plan then
        if reason == "NOT_ENOUGH_SPACE" then
            local sig = "split@" .. tostring(star.id) .. "@" .. tostring(rid)
            if offer_space_shift(sig) then return true end
        end
        return false
    end

    -- If the only user action was inserting this fresh ***, undo that insertion
    -- before opening our own block. Then one Ctrl+Z later returns directly to the
    -- pre-marker/pre-split project state. If safe rebasing is impossible, consume
    -- the marker inside our block as a fallback (that rare case may need two undos).
    local marker_insertion_undone = try_remove_new_star_from_undo(snap, star)

    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)

    if not marker_insertion_undone then
        reaper.DeleteProjectMarker(proj, star.id, false)
    end

    -- Move packed old/earlier chapters.
    for i = 1, #plan do
        local ch = plan[i]
        if abs(ch.delta) > EPS then
            for j = 1, #ch.items do
                local d = ch.items[j]
                reaper.SetMediaItemInfo_Value(d.item, "D_POSITION", d.s + ch.delta)
            end
        end
        if ch.region then
            write_entry_by_id(
                ch.region.id, true, ch.new_s, ch.new_e,
                ch.region.name, ch.region.color
            )
        end
    end

    -- Ordinary markers inside rigidly moved packages.
    for i = 1, #snap.markers do
        local m = snap.markers[i]
        local ch = chapter_for_marker_point(m.s, plan)
        if ch and abs(ch.delta) > EPS then
            write_entry_by_id(m.id, false, m.s + ch.delta, 0, m.name, m.color)
        end
    end

    -- The right half is a brand new chapter and stays where the user put it.
    local new_rid = reaper.AddProjectMarker2(
        proj, true, right_s, right_e, NEW_REGION_NAME, -1, 0
    )
    if new_rid < 0 then
        reaper.PreventUIRefresh(-1)
        reaper.Undo_EndBlock2(proj, "Split chapter FAILED", -1)
        error("AST chapter split: AddProjectMarker2 failed")
    end

    for i = 1, #right do
        set_item_home(right[i].item, new_rid)
    end
    for i = 1, #left do
        set_item_home(left[i].item, rid)
    end

    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, "Split chapter at ***", -1)

    local_split_pending[star.id] = nil
    return true
end

local function star_inside_existing_region(snap, star)
    for i = 1, #snap.regions do
        local r = snap.regions[i]
        if star.pos > r.s + EPS and star.pos < r.e - EPS then return true end
    end
    return false
end

local function process_new_region_star_splits(snap, components, active)
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
                last_blocker_signature = ""
                local changed = apply_existing_region_split(snap, components, active, split, star)
                -- A valid local split command owns this cycle even when it is
                -- waiting for +1h space; it must never fall through to A/B/C logic.
                return changed, true
            elseif res and res.status == "BLOCK" and star_inside_existing_region(snap, star) then
                local_split_pending[star.id] = true
                if not is_recording() then
                    local sig = "localsplitblock@" .. tostring(star.id) .. "@" .. tostring(res.reason)
                    if sig ~= last_blocker_signature then
                        last_blocker_signature = sig
                        local why = res.reason == "OVERLAP"
                            and "под маркером одновременно находится несколько айтемов"
                            or (res.reason == "DEEP_ITEM"
                                and string.format("маркер стоит слишком глубоко внутри айтема (%.1f сек от его начала)", res.offset or 0)
                                or "граница главы неоднозначна")
                        reaper.ShowMessageBox(
                            "Невозможно автоматически разделить главу по ***: " .. why .. ".\n\n" ..
                            "Разберите монтаж в районе маркера так, чтобы *** стоял в пустоте " ..
                            "или не дальше 15 секунд от начала единственного айтема.",
                            "Chapter automation",
                            0
                        )
                    end
                end
                return false, true
            elseif local_split_pending[star.id] then
                -- The user edited the project so the marker no longer divides an
                -- existing chapter. Release it back to ordinary pending-star logic.
                local_split_pending[star.id] = nil
            end
        end
    end
    return false, false
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
    end
    return which .. " *** имеет неоднозначную границу"
end

local function classify_pending_pair(snap)
    if #snap.stars < 2 then return nil end
    local ordered = stars_in_birth_order(snap)
    local A, B, C = ordered[1], ordered[2], ordered[3]

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
        return { status = "BLOCK", which = "Второй", res = {reason="ORDER"}, A=A,B=B,C=C }
    end

    local previous = items_between_boundaries(snap, ar.boundary, br.boundary)
    local current_right = C and resolve_star(snap, C, math.huge) or nil
    local current_limit = math.huge
    if C then
        if current_right and current_right.status == "VALID" then
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

    local region_ids = region_ids_in_items(snap, scan.previous)
    if #region_ids > 1 then return nil, "MULTIPLE_REGIONS" end

    local existing_rid = region_ids[1]
    local compaction = compute_compaction(scan.previous)
    if not compaction.s or not compaction.e then return nil, "EMPTY" end

    local target_region = existing_rid and snap.region_map[existing_rid] or nil
    local target = {
        rid = existing_rid,
        region = target_region,
        items = scan.previous,
        base_s = compaction.s - REGION_PAD,
        base_e = compaction.e + REGION_PAD,
        is_target = true,
        delta = 0,
        compaction = compaction
    }

    local anchor_start
    if #scan.current > 0 then
        local cmin = math.huge
        for i = 1, #scan.current do if scan.current[i].s < cmin then cmin = scan.current[i].s end end
        anchor_start = cmin - REGION_PAD
    else
        anchor_start = scan.B.pos
    end

    local packages = active_region_packages(snap, components, active)
    local plan, reason = plan_pack_before_anchor(packages, target, anchor_start)
    if not plan then return nil, reason end

    return {
        chapters = plan,
        target = target,
        anchor_start = anchor_start,
        existing_rid = existing_rid
    }
end

local function apply_finalize_plan(snap, scan, plan)
    local target = plan.target
    local target_final_delta = target.delta

    reaper.Undo_BeginBlock2(proj)
    reaper.PreventUIRefresh(1)

    -- Move earlier rigid chapter packages and target items. Target gets its internal
    -- >10 -> 9 s compaction first, then its global layout delta.
    for i = 1, #plan.chapters do
        local ch = plan.chapters[i]
        if ch == target then
            for j = 1, #target.items do
                local d = target.items[j]
                local base = target.compaction.pos[d.guid] or d.s
                reaper.SetMediaItemInfo_Value(d.item, "D_POSITION", base + target_final_delta)
            end
        elseif abs(ch.delta) > EPS then
            for j = 1, #ch.items do
                local d = ch.items[j]
                reaper.SetMediaItemInfo_Value(d.item, "D_POSITION", d.s + ch.delta)
            end
        end
    end

    -- Existing regions move with their packages. Target region (if it already
    -- exists) is resized to the compacted chapter; otherwise it is created below.
    for i = 1, #plan.chapters do
        local ch = plan.chapters[i]
        if ch.region then
            write_entry_by_id(
                ch.region.id, true, ch.new_s, ch.new_e,
                ch.region.name, ch.region.color
            )
        end
    end

    -- Ordinary markers follow their chapter. Target markers additionally receive
    -- the local compaction delta nearest to their original occupied component.
    for i = 1, #snap.markers do
        local m = snap.markers[i]
        local moved = false

        if m.s >= scan.ar.boundary - EPS and m.s < scan.br.boundary - EPS then
            local local_delta = target.compaction.delta_for_point(m.s)
            write_entry_by_id(
                m.id, false, m.s + local_delta + target_final_delta,
                0, m.name, m.color
            )
            moved = true
        end

        if not moved then
            local ch = chapter_for_marker_point(m.s, plan.chapters)
            if ch and ch ~= target and abs(ch.delta) > EPS then
                write_entry_by_id(m.id, false, m.s + ch.delta, 0, m.name, m.color)
            end
        end
    end

    local rid = plan.existing_rid
    if not rid then
        rid = reaper.AddProjectMarker2(
            proj, true, target.new_s, target.new_e,
            NEW_REGION_NAME, -1, 0
        )
        if rid < 0 then
            reaper.PreventUIRefresh(-1)
            reaper.Undo_EndBlock2(proj, "Finalize chapter FAILED", -1)
            error("AST finalize: AddProjectMarker2 failed")
        end
    else
        -- A live *** boundary is authoritative for FINALIZATION. Historical
        -- ownership may cross a star while the user is editing/moving material,
        -- but once A->B is finalized this region must forget every old-owned item
        -- outside the semantic A->B chapter or the follower could jump back to it.
        local target_set = {}
        for i = 1, #target.items do target_set[target.items[i].guid] = true end
        for i = 1, #snap.items do
            local d = snap.items[i]
            if d.home == rid and not target_set[d.guid] then
                set_item_home(d.item, nil)
            end
        end
    end

    for i = 1, #target.items do
        set_item_home(target.items[i].item, rid)
    end

    -- A is consumed only after the region definitely exists.
    reaper.DeleteProjectMarker(proj, scan.A.id, false)

    reaper.PreventUIRefresh(-1)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, "Finalize chapter and maintain 60s layout", -1)
    return true
end

local function handle_empty_pair(scan)
    if is_recording() then return false end
    local sig = string.format("empty@%d@%.6f@%d@%.6f", scan.A.id, scan.A.pos, scan.B.id, scan.B.pos)
    if sig == last_empty_signature then return false end
    last_empty_signature = sig

    reaper.ShowMessageBox(
        "Между первым и вторым *** нет айтемов.\n\n" ..
        "Первый *** будет удалён, второй останется началом текущей главы.",
        "Chapter automation",
        0
    )
    reaper.Undo_BeginBlock2(proj)
    reaper.DeleteProjectMarker(proj, scan.A.id, false)
    reaper.UpdateTimeline()
    reaper.UpdateArrange()
    reaper.Undo_EndBlock2(proj, "Remove empty chapter marker", -1)
    return true
end

local function process_pending_pairs()
    while true do
        local snap = read_snapshot()
        if sanitize_stars(snap) then
            -- Re-read after any marker deletion.
            goto continue
        end
        if #snap.stars < 2 then
            last_blocker_signature = ""
            last_empty_signature = ""
            return false
        end

        local scan = classify_pending_pair(snap)
        if not scan then return false end

        if scan.status == "BLOCK" then
            if not is_recording() then
                local sig = string.format(
                    "block@%d@%.6f@%d@%.6f@%s",
                    scan.A.id, scan.A.pos, scan.B.id, scan.B.pos,
                    tostring(scan.res and scan.res.reason)
                )
                if sig ~= last_blocker_signature then
                    last_blocker_signature = sig
                    reaper.ShowMessageBox(
                        "Автоматическое создание главы остановлено: " ..
                        blocker_text(scan.res, scan.which) .. ".\n\n" ..
                        "Разберите монтаж у этого маркера. Нормальный вариант — *** в пустоте; " ..
                        "допустимый вариант — внутри единственного айтема, но не дальше 15 секунд от его начала.",
                        "Chapter automation",
                        0
                    )
                end
            end
            return false
        end

        last_blocker_signature = ""
        if scan.status == "WAIT" then return false end

        if #scan.previous == 0 then
            return handle_empty_pair(scan)
        end

        if not pending_is_mature(scan) then return false end

        -- Build follower ownership only for layout protection; organizer selection
        -- itself is purely boundary-based and never depends on owner_by_component.
        local components = build_components(snap.items, PACK_GAP)
        clear_stale_homes(snap)
        bootstrap_unowned_regions(snap, components)
        components = build_components(snap.items, PACK_GAP)
        local active = choose_active_components(snap, components)

        local plan, reason = build_finalize_plan(snap, components, active, scan)
        if not plan then
            if reason == "EMPTY" then
                return handle_empty_pair(scan)
            elseif reason == "NOT_ENOUGH_SPACE" then
                local sig = string.format("finalspace@%d@%.6f@%d@%.6f", scan.A.id, scan.A.pos, scan.B.id, scan.B.pos)
                if offer_space_shift(sig) then
                    goto continue
                end
                return false
            elseif reason == "MULTIPLE_REGIONS" and not is_recording() then
                local sig = string.format("multirgn@%d@%d", scan.A.id, scan.B.id)
                if sig ~= last_blocker_signature then
                    last_blocker_signature = sig
                    reaper.ShowMessageBox(
                        "Между первым и вторым *** уже находятся айтемы нескольких разных регионов-глав.\n\n" ..
                        "Автоматически угадывать, какой регион уничтожать или объединять, скрипт не будет.",
                        "Chapter automation",
                        0
                    )
                end
                return false
            else
                return false
            end
        end

        last_space_signature = ""
        apply_finalize_plan(snap, scan, plan)
        -- A consumed; re-read. If C existed, B+C are now the normal pair.
        ::continue::
    end
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
    if #snap.stars == 0 then return false end

    local ordered = stars_in_birth_order(snap)
    local star = ordered[#ordered]
    if not star or local_split_pending[star.id] then return false end

    local res = resolve_star(snap, star, math.huge)
    if res.status ~= "VALID" or not res.starter then return false end
    local chapter_start = res.boundary

    local latest_end = nil
    for i = 1, #snap.items do
        local d = snap.items[i]
        if d.s >= chapter_start - EPS then
            if not latest_end or d.e > latest_end then latest_end = d.e end
        end
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
    reaper.Undo_EndBlock2(proj, "Update chapter 40/50 minute helpers", -1)
    return true
end

------------------------------------------------------------
-- One follower pass
------------------------------------------------------------

local function follower_pass(allow_prompts)
    local snap = read_snapshot()

    -- The hard cap still wins: a fourth insertion is rejected as the newest marker
    -- before it can act as a split command. With one/two/three stars, however, a
    -- fresh local split gets first refusal and cannot consume unrelated markers.
    if enforce_star_capacity(snap) then return true, false end

    local early_components = build_components(snap.items, PACK_GAP)
    local early_active = choose_active_components(snap, early_components)
    local split_changed, split_blocked = process_new_region_star_splits(snap, early_components, early_active)
    if split_changed then return true, false end
    if split_blocked then return false, true end

    if sanitize_stars(snap) then return true, false end
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
    -- script/REAPER restart. Ambiguous collisions still fall through to the old
    -- merge-or-undo dialog below.
    if transfer_detached_items_between_chapters(snap, components, active) then
        snap = read_snapshot()
        components = build_components(snap.items, PACK_GAP)
        active = choose_active_components(snap, components)
    end

    -- Critical ordering: an ambiguous collision prompt/rollback still happens
    -- before acquisition or region geometry writes.
    local mutated, blocked = handle_collisions(snap, components)
    if mutated then return true, false end
    if blocked then return false, true end

    if acquire_unowned_items(snap, components, active) then
        snap = read_snapshot()
        components = build_components(snap.items, PACK_GAP)
        active = choose_active_components(snap, components)
    end

    local neg_rid = first_negative_region_requirement(snap, components, active)
    if neg_rid then
        if allow_prompts and not is_recording() then
            if offer_space_shift("followerspace@" .. tostring(neg_rid)) then return true, false end
        end
        return false, true
    end
    last_space_signature = ""

    if not is_recording() then
        if delete_confirmed_orphans(snap, active) then return true, false end
    end

    update_region_geometry_and_color(snap, components, active)
    return false, false
end

------------------------------------------------------------
-- Monolithic cycle
------------------------------------------------------------

local function run_cycle(allow_prompts)
    -- 1) local split/follower/collision state first;
    -- 2) then insertion-ordered A/B/C organizer;
    -- 3) finally helper markers and geometry normalization.
    local changed, blocked = follower_pass(allow_prompts)
    if changed or blocked then return end

    process_pending_pairs()

    -- Final follower pass after organizer reflow so geometry/color is immediately
    -- normalized from the final project state.
    local changed2, blocked2 = follower_pass(false)
    if not changed2 and not blocked2 then
        update_duration_helper_markers()
    end
end

local function commit_prev_snapshot()
    prev_snap = read_snapshot()
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
    local_split_pending = {}

    last_collision_signature = ""
    last_blocker_signature = ""
    last_space_signature = ""
    last_empty_signature = ""

    mouse_was_down = false
    last_mouse_release = -math.huge

    -- First pass seeds persistent ownership from existing regions before any
    -- orphan can be deleted.
    run_cycle(false)
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

    local state = reaper.GetProjectStateChangeCount(proj)
    if state ~= observed_state then
        observed_state = state
        state_changed_at = now
    end

    if not mouse_gesture_active(now) then
        local recording = is_recording()
        local star_count = prev_snap and #prev_snap.stars or 0

        local recording_due = recording and star_count >= 2
            and (now - last_record_scan >= RECORD_SCAN_SEC)
        local star_due = star_count >= 2
            and (now - last_star_scan >= STAR_SCAN_SEC)
        local helper_due = recording and star_count >= 1
            and (now - last_helper_scan >= HELPER_SCAN_SEC)
        local settled_change = state ~= last_state and (now - state_changed_at >= SETTLE_SEC)

        if recording_due or star_due or helper_due or settled_change then
            run_cycle(not recording)
            commit_prev_snapshot()

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

