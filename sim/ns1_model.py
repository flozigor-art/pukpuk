"""Sample-exact Python model of ns1-clone.jsfx (matches the JSFX run in ysfx to ~1e-15).

fixed=False reproduces v3f, fixed=True reproduces v3g. Besides the audio output it
returns per-block internal state, which is what makes the failure visible.
"""
import numpy as np
from numba import njit

AMOUNT_POINT = np.array([0, 5, 10, 20, 30, 50, 75, 100], float)
SCALE_POINT = np.array([.05, .20, .32, .52, .75, 1, 1.3, 1.7])
CAP_AMOUNT = np.array([0, 5, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100], float)
CAP_DB = np.array([
    [0, -2.34, -4.56, -8.38, -12.85, -14.65, -15.56, -16.37, -17.41, -18.19, -21.97, -25.08],
    [0, -.10, -.15, -.22, -.30, -.40, -.50, -.60, -.75, -.88, -1.81, -3.12],
    [0, -.03, -.05, -.08, -.12, -.15, -.18, -.22, -.30, -.40, -.50, -.69],
    [0, -.02, -.03, -.05, -.09, -.10, -.13, -.15, -.17, -.18, -.19, -.20],
    [0, -.03, -.05, -.08, -.12, -.15, -.18, -.20, -.22, -.24, -.26, -.29],
    [0, -.10, -.20, -.40, -.70, -.80, -.90, -1, -1.10, -1.20, -1.50, -2],
    [0, -.11, -.34, -1.71, -1.91, -1.94, -1.96, -1.99, -2.05, -2.11, -3.05, -5.42],
    [0, -1.86, -3.41, -3.98, -5.06, -5.61, -6.15, -6.72, -7.35, -8.03, -13.09, -19.65]])
CROSSOVER = np.array([60, 120, 318.08, 930.61, 2725.29, 7984.37, 16000.])
B = 32


def _params(amount, srate):
    amount = min(100, max(0, amount))
    seg = 0
    for i in range(7):
        if amount >= AMOUNT_POINT[i + 1]:
            seg = i + 1
    seg = min(seg, 6)
    fr = (amount - AMOUNT_POINT[seg]) / max(AMOUNT_POINT[seg + 1] - AMOUNT_POINT[seg], 1e-6)
    scale = SCALE_POINT[seg] * (1 - fr) + SCALE_POINT[seg + 1] * fr
    floor = 1 if amount <= 0 else 10 ** (-18.6 / 20)
    base = 1 - (1 - floor) / (1 + (1 / max(scale, 1e-6) / 3) ** 4)
    cs = 0
    for i in range(11):
        if amount >= CAP_AMOUNT[i + 1]:
            cs = i + 1
    cs = min(cs, 10)
    cf = (amount - CAP_AMOUNT[cs]) / max(CAP_AMOUNT[cs + 1] - CAP_AMOUNT[cs], 1e-6)
    cap = 10 ** ((CAP_DB[:, cs] * (1 - cf) + CAP_DB[:, cs + 1] * cf) / 20)
    rb = lambda ms: 1 - np.exp(-B / (max(ms, .001) * .001 * srate))
    rates = np.array([rb(.10), rb(8.5), rb(1000), rb(50), rb(20), rb(2), rb(20), rb(3.5), rb(24), rb(10)])
    keep = np.exp(-2 * np.pi * np.minimum(CROSSOVER, srate * .45) / srate)
    commit = 1000 * .001 * srate / B
    return scale, floor, base, cap, rates, keep, commit, amount <= 0


@njit(cache=True)
def _run(x, scale, floor, base, cap, rates, keep, commit, bypass, fixed):
    n = x.shape[0]
    env_attack, env_release, noise_rise, noise_fall, noise_startup, voice_attack, voice_release, gain_open, gain_close, zcr_rate = rates
    feed = 1 - keep
    LP = np.zeros((2, 7)); band = np.zeros((2, 8)); ring = np.zeros((2, 8, B))
    peak = np.zeros(8); fast = np.zeros(8); noise = np.full(8, 1e-9); ratio = np.zeros(8)
    pre = np.full(8, 1e-9); quiet = np.ones(8); episode = 0; age = 0.0
    gain = np.minimum(base, cap).copy()
    vc = 0.0; zcr = 0.0; prev_pos = 1; zc = 0; acc = 0; rp = 0
    nb = n // B + 1
    L_vc = np.zeros(nb); L_acc = np.zeros(nb); L_noise = np.zeros((nb, 8)); L_fast = np.zeros((nb, 8)); L_gain = np.zeros((nb, 8))
    out = np.zeros((n, 2)); bi = 0
    for s in range(n):
        for c in range(2):
            d = x[s, c]; prev = 0.0
            for i in range(7):
                low = feed[i] * d + keep[i] * LP[c, i]; LP[c, i] = low
                band[c, i] = low if i == 0 else low - prev
                prev = low
            band[c, 7] = d - prev
        cp = 1 if band[0, 2] + band[0, 3] + band[0, 4] >= 0 else 0
        if cp != prev_pos:
            zc += 1
        prev_pos = cp
        wl = 0.0; wr = 0.0
        for i in range(8):
            g = 1.0 if bypass else gain[i]
            wl += ring[0, i, rp] * g; wr += ring[1, i, rp] * g
            ring[0, i, rp] = band[0, i]; ring[1, i, rp] = band[1, i]
            la = .5 * (abs(band[0, i]) + abs(band[1, i])) + 1e-12
            if la > peak[i]:
                peak[i] = la
        out[s, 0] = wl; out[s, 1] = wr
        rp += 1
        if rp >= B:
            rp = 0
            zcr += zcr_rate * (zc / B - zcr); zc = 0
            active = 0
            for i in range(8):
                er = env_attack if peak[i] > fast[i] else env_release
                fast[i] += er * (peak[i] - fast[i])
                up = noise_startup if acc else noise_rise
                nr = noise_fall if fast[i] <= noise[i] else up * (1 - vc)
                noise[i] += nr * (fast[i] - noise[i])
                ratio[i] = fast[i] / max(noise[i], 1e-12)
                if ratio[i] >= 2:
                    active += 1
                peak[i] = 0
            r1 = ratio[2]; r2 = ratio[3]; r3 = ratio[4]
            sm = r1 + r2 + r3 - min(r1, min(r2, r3)) - max(r1, max(r2, r3))
            t = min(1., max(0., (sm - 2) / 3)); rc = t * t * (3 - 2 * t)
            t = min(1., max(0., (zcr - .12) / .23)); zconf = 1 - t * t * (3 - 2 * t)
            acc = 1 if (vc < .30 and active >= 6 and zcr >= .20) else 0
            if fixed:
                for i in range(8):
                    if ratio[i] < 2:
                        quiet[i] = noise[i]
                if acc:
                    if not episode:
                        episode = 1
                        for i in range(8):
                            pre[i] = min(quiet[i], noise[i])
                    age = 0.0
                elif episode:
                    age += 1
                if episode:
                    for i in range(8):
                        pre[i] = min(pre[i], noise[i])
                    p1 = fast[2] / max(pre[2], 1e-12); p2 = fast[3] / max(pre[3], 1e-12); p3 = fast[4] / max(pre[4], 1e-12)
                    pm = p1 + p2 + p3 - min(p1, min(p2, p3)) - max(p1, max(p2, p3))
                    if zcr < .15 and pm >= 5:
                        for i in range(8):
                            noise[i] = min(noise[i], pre[i])
                        episode = 0
                    elif age >= commit:
                        episode = 0
            raw = rc * (.75 + .25 * zconf)
            vr = voice_attack if raw > vc else voice_release
            vc += vr * (raw - vc)
            for i in range(8):
                ad = 1 - (1 - floor) / (1 + (ratio[i] / max(scale, 1e-6) / 3) ** 4)
                t = min(1., max(0., (ratio[i] - 1.5) / 3.5)); ev = t * t * (3 - 2 * t)
                tg = min(base + (ad - base) * min(.95, vc * ev), cap[i])
                gr = gain_open if tg > gain[i] else gain_close
                gain[i] += gr * (tg - gain[i])
            L_vc[bi] = vc; L_acc[bi] = acc
            for i in range(8):
                L_noise[bi, i] = noise[i]; L_fast[bi, i] = fast[i]; L_gain[bi, i] = gain[i]
            bi += 1
    return out, L_vc[:bi], L_acc[:bi], L_noise[:bi], L_fast[:bi], L_gain[:bi]


def run(x, amount=50, srate=48000, fixed=True):
    """x: mono or (n, 2) float array. Returns dict with 'out' (n, 2) and per-block state."""
    if x.ndim == 1:
        x = np.stack([x, x], 1)
    o, vc, acc, noise, fast, gain = _run(np.ascontiguousarray(x, dtype=np.float64), *_params(amount, srate), fixed)
    return dict(out=o, vc=vc, acc=acc, noise=noise, fast=fast, gain=gain)
