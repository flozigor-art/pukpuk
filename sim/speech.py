"""Synthetic audiobook-like narration: phrases of syllables with fricatives, stop
closures, nasals, inhales before phrases, mouth clicks and room tone.

Every segment is labelled so the bench can measure the gain applied to vowels
(must stay open) and to pauses, breaths and clicks (should be suppressed)."""
import numpy as np
from scipy.signal import butter, sosfilt, lfilter

SR = 48000
VOWELS = {'a': [(700, 90), (1150, 110), (2600, 160), (3400, 250)],
          'o': [(500, 80), (900, 100), (2500, 150), (3400, 250)],
          'i': [(300, 60), (2250, 120), (3000, 200), (3700, 250)],
          'e': [(450, 70), (1900, 110), (2600, 160), (3500, 250)],
          'u': [(320, 60), (800, 90), (2300, 160), (3300, 250)],
          'm': [(250, 60), (1000, 300), (2200, 400), (3000, 400)]}


def db(x):
    return 20 * np.log10(np.maximum(x, 1e-20))


def rms(x):
    return np.sqrt(np.mean(x ** 2) + 1e-30)


def _reson(x, f, bw):
    r = np.exp(-np.pi * bw / SR); th = 2 * np.pi * f / SR
    return lfilter([1 - r], [1, -2 * r * np.cos(th), r * r], x)


def _env(n, att_ms, rel_ms, soft=False):
    e = np.ones(n); a = int(att_ms * SR / 1000); r = int(rel_ms * SR / 1000)
    if a > 0:
        e[:a] = np.linspace(0, 1, a) ** (2 if soft else 1)
    if r > 0:
        e[-r:] *= np.linspace(1, 0, r)
    return e


def _filt(x, lo, hi):
    if lo is None:
        sos = butter(2, hi, 'low', fs=SR, output='sos')
    elif hi is None:
        sos = butter(2, lo, 'high', fs=SR, output='sos')
    else:
        sos = butter(2, [lo, hi], 'band', fs=SR, output='sos')
    return sosfilt(sos, x)


def room(n, level_db, tilt, rng):
    """Room tone: mix of pink (tilt) and white noise. Note: v3f/v3g only learn a
    room tone that is bright enough (tilt <= ~0.4), see README."""
    w = rng.standard_normal(n)
    pink = lfilter([0.049922035, -0.095993537, 0.050612699, -0.004408786],
                   [1, -2.494956002, 2.017265875, -0.522189400], w)
    y = tilt * pink / rms(pink) + (1 - tilt) * rng.standard_normal(n)
    return y / rms(y) * 10 ** (level_db / 20)


class Narrator:
    def __init__(self, seed):
        self.r = np.random.default_rng(seed)

    def u(self, a, b):
        return self.r.uniform(a, b)

    def noise(self, dur, lo, hi, level, att, rel, soft=False):
        n = max(int(dur * SR), 64); y = _filt(self.r.standard_normal(n), lo, hi)
        return y / rms(y) * 10 ** (level / 20) * _env(n, att, rel, soft)

    def voiced(self, v, dur, f0, att, rel, level):
        n = int(dur * SR); t = np.arange(n) / SR
        f = f0 * (1 + .02 * np.sin(2 * np.pi * self.u(3, 6) * t + self.u(0, 6))) * (1 + np.linspace(0, self.u(-.15, .1), n))
        pulses = np.diff(np.floor(np.cumsum(f / SR)), prepend=0)
        y = lfilter([1], [1, -1.94, .9409], pulses)
        y = y / (np.abs(y).max() + 1e-12) + .02 * _filt(self.r.standard_normal(n), 500, None)
        for F, bw in VOWELS[v]:
            y = _reson(y, F * self.u(.92, 1.08), bw)
        y = np.diff(y, prepend=0)
        return y / (np.abs(y).max() + 1e-12) * 10 ** (level / 20) * _env(n, att, rel)

    def breath(self, level):
        d = self.u(.25, .6); a = d * 1000 * self.u(.4, .8); r = self.u(30, 90)
        kind = self.r.integers(3)
        if kind == 0:
            return self.noise(d, 300, None, level, a, r, True)
        if kind == 1:
            return self.noise(d, 800, 7000, level, a, r, True)
        y = self.noise(d, 250, 6000, level, a, r, True)
        return y + self.noise(d, None, 150, level + self.u(-6, 6), d * 500, 40, True)  # air on the capsule

    def fricative(self, kind, level):
        d = self.u(.06, .16); a = self.u(8, 25); r = self.u(8, 20)
        lo, hi, trim = {'s': (4000, None, 0), 'sh': (1700, 6500, 0), 'f': (900, None, -3), 'h': (700, 4500, -4)}[kind]
        return self.noise(d, lo, hi, level + trim, a, r)

    def click(self, level):
        n = int(self.u(.0005, .003) * SR); y = _filt(self.r.standard_normal(n), 1500, None)
        return y / rms(y) * 10 ** (level / 20) * _env(n, 0, .3)


def narration(seed, room_db=-66, tilt=.2, phrases=8, level=-16):
    """Returns (noisy signal, labels, segment boundaries). 4 s of room tone first,
    so the suppressor has learned the floor before the first phrase."""
    g = Narrator(seed); parts = []; labels = []

    def add(sig, lab):
        parts.append(sig); labels.append(lab)
    add(np.zeros(4 * SR), 'pre')
    f0 = g.u(85, 210)
    for _ in range(phrases):
        pause = g.u(.25, 1.2)
        if g.u(0, 1) < .7:
            add(np.zeros(int(max(0, pause - .5) * SR)), 'pause')
            add(g.breath(level + g.u(-40, -12)), 'breath')
            add(np.zeros(int(g.u(0, .12) * SR)), 'gap')
        else:
            add(np.zeros(int(pause * SR)), 'pause')
        if g.u(0, 1) < .3:
            add(g.click(level + g.u(-30, -10)), 'click')
            add(np.zeros(int(g.u(.01, .08) * SR)), 'gap')
        lvl = level + g.u(-8, 4)
        for k in range(g.r.integers(4, 16)):
            decl = -k * g.u(0, .6)                      # phrase declination
            c = g.r.choice(['', '', 's', 'sh', 'f', 'h', 't', 'k', 'm'])
            if c in ('s', 'sh', 'f', 'h'):
                add(g.fricative(c, lvl + decl + g.u(-22, -8)), 'fricative')
            elif c in ('t', 'k'):
                add(np.zeros(int(g.u(.02, .06) * SR)), 'closure')
                add(g.noise(g.u(.008, .025), 1000, None, lvl + decl + g.u(-20, -8), 1, 5), 'burst')
            elif c == 'm':
                add(g.voiced('m', g.u(.05, .1), f0, g.u(3, 30), 5, lvl + decl - 10), 'nasal')
            add(g.voiced(g.r.choice(list('aoieu')), g.u(.07, .25), f0, g.u(3, 40), g.u(5, 30), lvl + decl + g.u(-4, 2)), 'vowel')
            if g.u(0, 1) < .25:
                add(np.zeros(int(g.u(.01, .08) * SR)), 'gap')
    add(np.zeros(int(.8 * SR)), 'pause')
    pos = np.cumsum([0] + [len(p) for p in parts])
    x = np.concatenate(parts)
    return x + room(len(x), room_db, tilt, np.random.default_rng(seed + 1000)), labels, pos
