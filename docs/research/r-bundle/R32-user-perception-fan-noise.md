# R32 — User-Perception Thresholds for Fan Noise

**Status:** Research bundle (R32)
**Subject:** dBA targets for ventd's "quietness target" preset
**Audience:** ventd designers wiring the preset UI; calibration loop authors who decide when to stop reducing fan duty.
**Scope:** What numeric dBA caps the daemon should offer as canned defaults, what numbers users actually type when given a free-form field, and what acoustic floor makes further reduction pointless.

---

## 1. Executive Summary

- **Three preset tiers cover ~95% of plausible user environments**: a "Whisper" tier (<=25 dBA at the user's ear) for shared bedrooms and night use, an "Office" tier (<=32 dBA) for daytime focus work, and a "Performance" tier (<=45 dBA) where audible fans are acceptable in exchange for sustained clocks. These map cleanly onto the WHO bedroom guideline (30 dBA), EPA indoor activity-interference threshold (45 dBA indoors), and Silent PC Review's long-running editorial thresholds (15-20 dBA idle / 20-27 dBA load) for "silent" vs "quiet" PCs.
- **The hard perceptual floor is around 18-22 dBA at 1 m in a typical room**, set by ambient HVAC, fridge hum, traffic infiltration, and the user's own breathing. Below that, additional fan reduction is psychoacoustically unrecoverable: it sits below the room's noise floor and below the just-noticeable-difference (JND) of 1 dB for broadband sound. ventd's controller should treat ~20 dBA as a hard "stop reducing" gate when users specify lower values, and should warn the user when their cap is below plausible ambient.
- **Tonal content (a single fan whine at one RPM) is approximately 4-6 dB more annoying than equivalent broadband noise** at the same dBA. This argues for the daemon to penalize tonal regions of the fan curve internally — i.e. avoid duty cycles where bearing whine, blade-pass tones, or PWM ticks dominate, even if the overall dBA is within budget. Any "<=N dBA" preset should be implemented as "<=N dBA AND avoid the user's tonal-resonance bands", not pure dBA.
- **Users gravitate to round numbers when typing: 25, 30, 35, 40 dBA dominate community discussions**; "32 dBA" is a common mentioned breakout level where fan noise becomes annoying in a quiet room. The presets should be labeled with familiar reference comparisons ("library", "quiet office", "normal conversation") because most users do not have an intuition for what 30 dBA sounds like.
- **dBA alone is the wrong metric for any value < 35 dBA**. At quiet residential levels A-weighting under-reports low-frequency hum, which is the dominant residual fan signature. ventd should record loudness in dBA for the UI but internally consider broadband + low-frequency content; a 25 dBA cap with a 60 Hz hum at 35 dB SPL is not a "quiet" outcome.

---

## 2. Background Reference Levels (Ambient Environments)

Numbers are A-weighted sound pressure level, free-field, measured at the listener position. Sources: WHO Environmental Noise Guidelines for the European Region (2018), WHO Night Noise Guidelines for Europe (2009), US EPA "Levels Document" (1974), ANSI/ASA NC and RC criteria, ASHRAE Handbook ch. 49.

| Environment | Typical L<sub>Aeq</sub> (dBA) | Source / standard | Notes |
|---|---|---|---|
| Anechoic chamber (Microsoft Building 87, world record) | -24.9 dBA | bigsoundbank.com / Guinness | Below threshold of human hearing; most listeners hear their own bloodstream. |
| Threshold of hearing (1 kHz, normal listener) | 0 dBA | ISO 226:2023 | Reference 20 µPa. |
| Empty rural house at night | ~20-25 dBA | WHO 2009 / field surveys | Floor set by HVAC standby + outside infiltration. |
| Quiet bedroom at night (target) | **<=30 dBA** | WHO 1999/2009 | "Less than 30 dB(A) in bedrooms during the night for sleep of good quality." |
| Quiet residential indoor (day) | 30-40 dBA | Noise Awareness Day; ASHRAE NC-25/30 | Library = 40; "quiet residential area" indoor = 40. |
| EPA indoor "activity-interference and annoyance" cap | 45 dBA | EPA 1974 Levels Document | Beyond this, daily activities (conversation, work, sleep) start to be impaired. |
| Typical home office | 40-50 dBA | WELL v2; ASHRAE | NC-30 to NC-40. |
| Refrigerator at 1 m | ~50 dBA | Noise Awareness Day | Frequently the loudest steady source in a kitchen. |
| Open-plan office | 49-58 dBA (NC-40 to NC-50) | ASHRAE handbook ch.49; NRC Canada | Most-preferred ambient in studies: ~45 dBA; should not exceed 48 dBA for comfort. |
| Normal conversation (1 m) | 60-65 dBA | Yale EHS chart; NIOSH | |
| Vacuum cleaner (1 m) | 60-85 dBA | Noise Awareness Day | |
| LAN party / busy gaming room (estimated) | 60-75 dBA | extrapolated from open-office + multi-machine fan load; no formal study found | Comparable to a small server closet. |
| Server rack (1 m, single high-density) | 75-80 dBA | Soundtrace / Schneider 2019 | |
| Data center cold-aisle | 85-92 dBA | Soundtrace; SE Blog 2019 | Above OSHA action level. |
| Server-room interior of a hot-aisle row | up to 96 dBA | Soundtrace | Hearing-conservation territory. |

### Key reference standards (for citation in the UI's "learn more" link)

- **WHO Environmental Noise Guidelines for the European Region (2018)** — recommends Lnight outside <40 dB(A) and indoor bedroom <30 dB(A) for sleep; classroom <35 dB(A) for learning.
- **EPA 550/9-74-004 "Levels Document" (1974)** — 55 dBA outdoor / 45 dBA indoor as activity-interference and annoyance thresholds.
- **ISO 1996-1:2016 / 1996-2:2017** — defines L<sub>Aeq</sub>, L<sub>Amax</sub>, and tonal/impulsive penalties for environmental noise.
- **ISO 226:2023** — equal-loudness contours; the basis for A-weighting's deviation from perceived loudness at low SPLs and low frequencies.
- **ANSI S12.2 / ASHRAE Handbook ch.49** — NC and RC curves for indoor mechanical-system noise; bedrooms NC-25 to NC-30, private offices NC-30 to NC-35, open offices NC-40 to NC-45.

---

## 2.5 Psychoacoustics primer (the math behind the numbers)

A short reference for spec authors who need to justify the choices in §9. Skip if you are already fluent in dB/dBA/loudness.

### 2.5.1 dB, dBA, and dBC

- **dB SPL** is `20 * log10(p / p_ref)` where `p_ref = 20 µPa`. Pure pressure measurement, no perceptual weighting.
- **dBA** is dB SPL after applying the A-weighting filter: a frequency-dependent gain that approximately matches the inverse of the 40-phon equal-loudness contour. Roughly: -39 dB at 50 Hz, -16 dB at 250 Hz, 0 dB at 1 kHz, +1 dB at 2.5 kHz, -1 dB at 8 kHz, -10 dB at 16 kHz.
- **dBC** uses a much flatter weighting (-3 dB at 32 Hz, 0 dB at 1 kHz, -3 dB at 8 kHz). dBC - dBA is a quick-and-dirty low-frequency-content indicator: a value > 15 dB suggests a low-frequency-dominated source.
- **Sound power level (L<sub>W</sub>)** vs **sound pressure level (L<sub>p</sub>)**: manufacturers sometimes spec sound power (a property of the source). For a typical fan in a free field, L<sub>p</sub> at 1 m ≈ L<sub>W</sub> - 11 dB. Most enthusiast specs are L<sub>p</sub> at 1 m in a near-anechoic chamber.

A-weighting is **right** for steady mid-band noise around moderate SPLs (40-80 dBA). It is **wrong** for low-SPL, low-frequency, or tonal noise — exactly the regime ventd cares about. This is why the recommendations in §8 use dBA as the user-facing metric but augment it internally with dBC and tonality.

### 2.5.2 Equal-loudness contours (ISO 226)

The Fletcher-Munson curves (1933), Robinson-Dadson (1956), and the modern ISO 226:2003 / 226:2023 contours specify, for each frequency, the SPL needed to produce an equal-loudness sensation as a 1 kHz reference tone. Key takeaways for fan noise:

- At 40 phon (a soft-conversation reference), 100 Hz needs ~62 dB SPL to sound as loud as 40 dB SPL at 1 kHz. That is a 22 dB sensitivity gap.
- At 20 phon (whisper-quiet), the gap widens further: 100 Hz needs ~70 dB SPL, a 50 dB difference.
- Above 1 kHz the contours flatten (and dip slightly around 3-4 kHz where the ear canal resonates). High-frequency hiss above 5 kHz has nearly the same perceived loudness per dB as 1 kHz.
- Below 100 Hz the contours steepen sharply: at 50 Hz, you need ~42 dB SPL for the *threshold* of hearing.

**Implication:** A fan whose dominant tone is at 80 Hz can be 30 dB SPL "louder" than a hiss-dominated fan and still measure the same dBA. The user with normal hearing will hear both, but report the bass-dominant one as more annoying because it has more low-frequency energy *per* perceived loudness unit.

### 2.5.3 Loudness in sones / phons

The **sone** scale (Stevens 1957) is calibrated so that doubling the sone count corresponds to a perceptual doubling of loudness. Empirically:

- 1 sone = 40 phon = 40 dB SPL at 1 kHz
- 2 sones = 50 phon (twice as loud)
- 4 sones = 60 phon (four times as loud)

I.e., **+10 phon = perceptual doubling**. This is why the textbook claim "10 dB sounds twice as loud" is roughly right for steady mid-band noise. A fan that goes from 30 dBA to 40 dBA is *twice as loud*; from 30 to 33 is *slightly louder* (perception of a few sones difference); from 30 to 31 is *barely perceptible*.

For ventd's UI, a sane mental model for the user is:
- **+1 dBA** = JND (you might notice on careful A/B).
- **+3 dBA** = clearly louder.
- **+10 dBA** = roughly twice as loud.
- **-10 dBA** = roughly half as loud.

A preset slider should therefore probably display a coarser "subjective" axis on top of the linear dBA axis, with named tick marks at dBA values that are perceptually distinct.

### 2.5.4 Masking

Two simultaneous sounds at similar frequencies do not simply add in dB; the louder one masks the quieter. Critical-band masking effects (Fletcher 1940; Zwicker bandgroups) say:

- A masker raises the threshold of audibility for sounds in the same critical band by roughly its own level minus a few dB.
- Critical bands are ~100 Hz wide below 500 Hz, then about 1/6-octave above.
- For broadband fan noise, the entire spectrum acts as a self-masker; tonal peaks within it must clear the surrounding broadband floor by ~5-6 dB before they become audible (this is the basis of the ECMA-74 / ISO 7779 prominent-tone definition).

**Implication for ventd:** the daemon's tonality detector should be running a per-band masking calculation, not a global tone-detection. A 110 Hz peak that is 4 dB above the surrounding 80-150 Hz broadband level is *not* a prominent tone and need not be penalized. A 110 Hz peak that is 8 dB above the surrounding broadband is prominent and must be penalized.

---

## 3. Perceptual Thresholds — When does fan noise become noticeable, annoying, stressful?

### 3.1 Detection (above ambient)

The audibility of fan noise is governed by **signal-to-noise ratio above the ambient floor**, not absolute SPL. Standard psychoacoustic detection thresholds:

| Function | SNR (signal above masker) | Source |
|---|---|---|
| Detect presence of broadband signal 50% of the time | ~0 dB SNR for matched-spectrum maskers; -3 to +3 dB typical | DOSITS / Plomp |
| Discriminate between two signals | +3 dB above detection | DOSITS detection-threshold ladder |
| Recognize a specific sound (e.g. "that's a fan") | +3 dB above discrimination (~+6 dB above detection) | DOSITS |
| Comfortable speech communication | +15 dB SNR | DOSITS |
| Awakening probability rises sharply | event >= +6 dB above background L<sub>night</sub> | Basner 2006; WHO sleep review 2018 |

**Operational implication for ventd:** if ambient is 30 dBA, a fan at 30 dBA is at threshold (50% detection), at 33 dBA it is reliably discriminable, and at 36+ dBA it is recognizable as a fan. A user in a 25 dBA bedroom typing a "30 dBA cap" is asking for a fan that is recognizable (+5 dB above ambient) but not yet annoying.

### 3.2 Just-noticeable difference (JND) for level changes

| Stimulus | JND in dB | Source |
|---|---|---|
| Pure tone at moderate SPL | 0.2 - 0.4 dB | Florentine & Buus 1981; ScienceDirect |
| Broadband noise, normal-hearing listeners | 0.7 - 0.9 dB | Jesteadt et al. 1977; PMC review |
| Broadband noise, hearing-impaired listeners | ~1.4 dB | Jesteadt, ibid |
| Practical "smallest change you can hear" rule | 1 dB | Standard textbook (Fastl & Zwicker, *Psychoacoustics*) |
| Subjective "twice as loud" change | ~10 dB | Stevens 1957 power law; sengpielaudio |
| Subjective "clearly louder/quieter" | 3 dB (doubling/halving acoustic power) | textbook rule |

**Operational implication:** ventd's quietness target should not bother trying to reduce fan noise by less than ~1 dB at the listener position. Below that, the user cannot tell. Per Weber-Fechner (the smallest-fraction-of-stimulus rule), this is roughly correct across the 20-50 dBA range for broadband noise, with a very mild "near-miss" improvement at higher SPL.

### 3.3 Annoyance and stress

The Schultz curve (1978) and its successors (Miedema & Vos 1998; FICAN 2018) relate **percentage highly annoyed (%HA)** to long-term noise exposure (L<sub>dn</sub> or L<sub>den</sub>). Translated to indoor steady fan noise:

- L<sub>Aeq</sub> < 30 dBA indoor: ~0% highly annoyed.
- L<sub>Aeq</sub> 30-35 dBA indoor: 1-5% highly annoyed in residential settings.
- L<sub>Aeq</sub> 35-45 dBA indoor: 5-15% highly annoyed; cited as "activity interference threshold" by EPA.
- L<sub>Aeq</sub> 45-55 dBA indoor: 15-30% highly annoyed; sleep disturbance probability rises.
- L<sub>Aeq</sub> > 55 dBA indoor: substantial annoyance; speech communication impaired without raising voice.

These %HA numbers are aircraft/road-noise-derived and are **conservative for steady fan noise**, which has lower information content than transportation noise. Practical experience (Silent PC Review's editorial position, see §5) is that PCs measured at 20 dBA at 1 m are "effectively inaudible in most rooms," 27 dBA is "noticeable but not annoying," and >30 dBA at the listener crosses into "annoying for desktop work."

### 3.4 Sleep and health

WHO 2018 and the systematic review (PMC5877064) report:

- L<sub>night, indoor</sub> > 30 dBA L<sub>Aeq</sub>: detectable sleep-architecture changes (more cortical arousals, lower N3).
- L<sub>Amax</sub> events 35-40 dBA: probability of awakening rises measurably, particularly in light sleep.
- Continuous noise at 40+ dBA L<sub>Aeq</sub> in bedrooms: chronic risk markers (cortisol, BP) emerge in long-term studies.

**Implication for ventd's "Whisper" preset:** if marketed for night/bedroom use, the cap must be <=30 dBA L<sub>Aeq</sub> and the daemon should also constrain L<sub>Amax</sub> (no transient ramp spikes >35 dBA), since a single fan ramp event during a sleep cycle can wake a sleeper even if the long-term average is fine.

### 3.5 Why ramp events matter as much as steady levels

An overlooked finding from the WHO 2018 sleep review (Basner et al.; PMC5877064): **awakening probability is a function of the rise-rate and the headroom over background, not just the peak**. The same 40 dBA event:

- played as a smooth 5 s ramp from 30 dBA: low awakening probability (<10% per event in N2 sleep).
- played as a 200 ms step from 30 to 40 dBA: substantially higher awakening probability (30-50%).
- played as the 6 dB-above-background trigger figure cited in §3.1: dose-response is reliable across road, rail, and aircraft noise.

For a bedroom HTPC or a workstation that the user occasionally walks past in the night, the daemon should:
- Cap the per-second slew rate at +3 dB/s (consistent with §7.5).
- Treat any sudden RPM step >300 RPM as a "ramp event" and log it.
- In Whisper preset, prefer a slightly higher steady fan to avoid future ramps, rather than aggressively spinning down then up. This is counter-intuitive but matches sleep-research evidence.

---

## 4. Fan-Specific Perception — Tonal vs Broadband

### 4.1 Why fan noise is special

PC and laptop fans produce a mixed spectrum:

- **Broadband turbulent flow noise**: smooth, hiss-like, well-described by dBA, mostly above 500 Hz, scales with tip speed^5 to ^6.
- **Blade-pass tone (BPF)**: f = (RPM/60) * blade_count. A 7-blade fan at 1500 RPM has a 175 Hz fundamental + harmonics. This is the "whine."
- **Bearing/motor tones**: low-Q peaks from rotor harmonics, PWM switching artifacts, sleeve/FDB resonances. Often 10-200 Hz.
- **Structure-borne**: fan-mount resonances coupling to chassis panels at specific RPM. Dependent on physical mount, not psychoacoustically intrinsic to the fan.

### 4.2 Tonal vs broadband annoyance

Multiple peer-reviewed studies converge on a **tonal penalty of 4-6 dB**, meaning a tonal noise at level L is judged equally annoying as a broadband noise at L+5 dB:

- Pedersen et al. (2017), *Annoyance of low-level tonal sounds — A penalty model*, Applied Acoustics: penalty 0-12 dB depending on tonal frequency, peak around 2 kHz.
- Hongisto et al. (multiple), reviewed by ABD Engineering: tonal noise typically 4 dB more annoying.
- Lee et al. (2021, MDPI IJERPH) on low-frequency tonal components: penalty 0-7 dB in residential bedtime conditions (background 25-30 dB), increasing with tonal frequency.
- ISO 1996-2 specifies a regulatory penalty up to +6 dB for tonal noise.

**The penalty is largest in quiet environments**: when background is 25 dBA (residential night), a 200 Hz tone gets up to 7 dB more annoyance penalty than the same level in broadband. In a 50 dBA office, the same tone disappears into the masker.

### 4.3 Implications for ventd's controller

1. **A pure dBA cap is insufficient.** A 30 dBA fan with a strong 120 Hz blade-pass tone may be perceived as roughly equivalent to a 35 dBA broadband fan with no tone.
2. **Tonal penalties should be subtracted from the user's effective budget** in the daemon's internal accounting. If the user types "32 dBA" and the current fan signature has a strong BPF, treat it as if 4-5 dB of the budget is already spent.
3. **The fan curve has tonal "valleys" and "peaks"**. Some RPMs put the BPF into a structural resonance of the chassis or cooler; others don't. The daemon should *measure* tonality (e.g. via a tone-to-noise ratio per ECMA-74 / ISO 7779 method, or simpler peak-to-broadband ratio in the FFT) and avoid RPM bands where TNR > ~5 dB.
4. **A-weighting under-counts low-frequency tones.** The 60-200 Hz range, where many fan tones live, is attenuated by 16-30 dB by A-weighting. A 30 dBA reading can hide a 50 dB SPL hum at 80 Hz that the user *will* hear and complain about. Internally use **C-weighted or unweighted band levels** for the tonality check; only display dBA in the UI because that's what users have intuition for.

---

## 5. PC Enthusiast Community Baseline Expectations

### 5.1 Silent PC Review (the editorial gold standard, 2002-present)

SPCR has published explicit thresholds for component recommendation, repeated across many articles:

> "Silent PCs should measure 15 dBA@1m or lower at idle and 20 dBA@1m or lower at maximum load."
> "Quiet PCs should measure 20 dBA@1m or lower at idle and 27 dBA@1m or lower at maximum load."
> Components exceeding 30 dBA@1m SPL are excluded from recommendations.

These are measured at 1 m from the case in a 17 dBA ambient anechoic-grade chamber. Translated to the desk position (typically 0.5-0.7 m from the PC), levels are 3-4 dB higher.

### 5.2 Manufacturer specs

| Product | Manufacturer noise spec | Notes |
|---|---|---|
| Noctua NF-A12x25 PWM (max RPM, 2000) | 22.6 dBA | Acoustic measurement chamber, 1 m |
| Noctua NF-A12x25 PWM with LNA | 18.8 dBA | Low-noise adapter 1700 RPM |
| Noctua NF-S12A PWM (max) | 17.8 dBA | The "library" fan |
| Noctua NF-A14 FLX (max) | 19.2 dBA | |
| Noctua NH-U12S CPU cooler (max) | 22.4 dBA | |
| be quiet! Silent Wings 4 120mm (standard) | 18.9 dB(A) | |
| be quiet! Silent Wings 4 120mm high-speed | 31.2 dBA | |
| be quiet! Silent Wings Pro 4 140mm | 36.8 dBA | At max; designed for speed not pure silence |

These are at maximum RPM. Actual operating noise for a thermally-controlled case fan at 30-50% PWM is typically 8-15 dB lower, putting good fans well under 15 dBA at idle.

### 5.3 Forum consensus (Overclock.net, AnandTech, Tom's Hardware threads 2002-2024)

A consistent informal taxonomy emerges:

| Range | Subjective label (community consensus) |
|---|---|
| <=15 dBA | "Inaudible" — only perceptible in a sound-treated room |
| 15-20 dBA | "Whisper-quiet" — near-floor of typical living spaces |
| 20-25 dBA | "Very quiet" — barely audible in a normal room |
| 25-30 dBA | "Quiet" — noticeable but unobtrusive |
| 30-35 dBA | "Audible" — clearly present, may distract |
| 35-40 dBA | "Noticeable / mildly annoying" — common for stock cooling under light load |
| 40-45 dBA | "Loud" — typical gaming-PC stock cooling |
| >45 dBA | "Very loud" — server-style or thermally-stressed laptops |

Common quote (paraphrased from Tom's Hardware, AnandTech threads): "32 dBA seems to be the breakout level where annoyance begins."

### 5.3.1 Why community and standards numbers diverge

A reader comparing §2 (standards) and §5.3 (community taxonomy) will notice that the community calls 30 dBA "quiet" while the WHO standard treats 30 dBA as the *upper bound* for healthy sleep. Three reasons:

1. **Standards are population-conservative.** WHO chooses the level at which 95% of the population is unaffected. Community taxonomies are calibrated for adult enthusiasts in their own homes during the day — a much narrower population.
2. **Standards are L<sub>night</sub> long-term, community is acute SPL.** WHO 30 dBA is averaged over a sleep period; community "30 dBA is quiet" usually means an instantaneous SPL during waking work.
3. **Self-selection.** PC enthusiasts on forums are the people who *care* about noise enough to post; their sensitivity is higher than the general population. Their "annoying at 32 dBA" is a stronger statement than a random user's would be.

This means ventd's preset table should not strictly equal the WHO numbers, but should sit close to them with the community taxonomy as the actual operating reference. Office-quiet at 32 dBA is the right default for the same population that *is* the typical Linux ventd user.

### 5.4 What users actually type

When given a free-form numeric field, community discussions show users gravitate toward round numbers:

- **25 dBA** — picked by users who already know SPCR's "very quiet" threshold or are bedroom/HTPC builders.
- **30 dBA** — the most common single number; aligns with WHO bedroom guideline. Often picked by users transitioning from "stock everything" to a quiet build.
- **32 dBA** — picked by users who saw "32 dBA breakout" in a forum and want to stay below it. Surprisingly specific.
- **35 dBA** — daytime-only office target; typical of users who say "I just don't want to hear it during work."
- **40 dBA** — pragmatic gamer / workstation target; user has accepted some audible noise.
- **45 dBA** — performance-first user who wants the daemon to *cap* turbine-mode behavior, not to make the PC quiet.

ventd's preset UI should anchor on these numbers. A free-form field with a slider stepped at 1 dBA, defaulting to 30 dBA, with named anchors at 25/30/35/40/45, will match community intuition.

---

## 5.5 Worked example: translating a forum post into a preset

A representative Tom's Hardware post (paraphrased from "Is a 32 decibel case fan loud?"):

> "I'm building a quiet PC and I see this fan rated at 32 dBA at full speed. Is that going to be loud? My room is pretty quiet, I just want to be able to work without hearing the PC."

How should ventd's preset UI receive this user?

1. The fan spec is **at 1 m at full speed in an anechoic-grade chamber**. The user will sit ~50 cm from the fan, in a room with ~25 dBA ambient. Add ~6 dB for distance halving, subtract ~3-5 dB for typical PWM-controlled real-world duty (most case fans run at 30-50% PWM), net change ≈ +1 to +3 dB at the user's ear vs the spec.
2. Effective at-ear level: ~33-35 dBA. This is above the user's ambient by 8-10 dB — comfortably above detection threshold (recognition territory), but below the 45 dBA EPA activity-interference threshold.
3. ventd's "Office-quiet (<=32 dBA)" preset would have the daemon throttle this fan to roughly 70-80% of the spec, i.e. ~28 dBA at 1 m, which translates to ~30-32 dBA at the user's ear.
4. The user's mental model — "I just want to be able to work without hearing the PC" — maps to "Office-quiet" in our preset table. The label and the tooltip should make that mapping obvious.

The same fan, on the "Whisper (<=25 dBA)" preset, would need the daemon to clamp it down to ~50% PWM if achievable. On the "Performance (<=45 dBA)" preset, ventd would let it run at full speed because it is well within the cap.

This worked example shows why the preset names matter more than the numbers: the average user does not know what 32 dBA *sounds* like, but they know what "office-quiet" means.

---

## 6. OEM / Laptop Precedents

These are NotebookCheck and review-press measurements (15 cm from device, fan center of laptop), which are higher than free-field 1 m measurements by ~10-15 dB. They are the relevant numbers for laptop-class hardware and tell us what users *experience*.

| Class | Idle (dBA) | Sustained load (dBA) | Source |
|---|---|---|---|
| Apple MacBook Air (fanless M-series) | ~20-22 (room floor) | ~20-22 | Multiple reviews |
| Apple MacBook Pro 14"/16" M-series, light load | 22-25 | 32-42 | NotebookCheck; macperformanceguide |
| Apple MacBook Pro M3 Max under sustained AI/render | up to 47-58 | macperformanceguide reports "intolerably loud fan noise" |
| Dell XPS 13 (recent generations) idle | 33-35 | 45-47 | HotHardware reviews |
| ThinkPad X1 Carbon idle | ~28-30 | ~38-42 | NotebookCheck typical |
| ASUS ROG Zephyrus G14 (Quiet/Silent profile) | 28-30 | 35 (Quiet mode) / 45-50 (Performance) | UltrabookReview |
| Generic gaming laptop, performance mode | 35-40 | 50-55 | Jarrod's Tech laptop fan-noise comparison |

**Implication:** an "Office-quiet" preset of 32 dBA at the user's ear corresponds roughly to what Apple ships as the load-noise of a MacBook Pro under moderate work — a level that millions of users have effectively voted "acceptable" by buying the device. ventd should not chase floors below what the best-tuned consumer laptop achieves.

### 6.1 NotebookCheck measurement methodology (and why their numbers run high)

NotebookCheck — the largest single source of consistent laptop noise data — measures with the microphone 15 cm from the device center, vibration-isolated, in a quiet room, reporting dB(A) SPL. They report five scenarios: idle minimum, idle median, idle maximum, load maximum (Prime95), and gaming (Cyberpunk 2077 1080p Ultra). They also publish a frequency spectrum to indicate whether the noise is high- or low-frequency dominated.

The 15 cm measurement distance is deliberately closer than the user's actual position (typical laptop work is 30-60 cm from the deck) and so NotebookCheck numbers run **5-8 dB higher** than what the user experiences. A NotebookCheck "load max 50 dBA" laptop is roughly a 42-45 dBA experience at the user's chest. The same correction applies — in reverse — to manufacturer specs that quote 1 m anechoic numbers; user experience is closer.

ventd's preset numbers are intended as **at the user's ear, in their actual room**, which means:

- For a desktop, the daemon should target ~5-8 dB *above* the manufacturer's 1 m anechoic spec for an equivalent at-ear value (because the user sits closer and their room has reflective surfaces).
- For a laptop, the daemon should target ~3-5 dB *below* NotebookCheck's published 15 cm number for an equivalent at-ear value.

These corrections are rough and depend strongly on chassis geometry. They underscore the §11 floor logic: don't trust ambient/at-ear estimation below ±5 dB without dedicated calibration.

### 6.2 What "fanless" means

The Apple MacBook Air M-series, Surface Pro X (ARM), several Dell Latitude 7-series, and the Framework 13 (with custom config) are nominally fanless. Their measured idle SPL is indistinguishable from the room floor (~20-22 dBA in a quiet room), set by the listener's own physiology and by occasional coil whine or PSU buzz.

This sets a hard ceiling on what ventd can deliver. **A user sitting next to a fanless laptop hears ~22 dBA — and that is what "as quiet as possible" means in 2026.** Any ventd preset claiming "<=20 dBA" is essentially claiming to make a desktop quieter than a fanless ARM laptop, which is achievable only in a sound-treated room with a passive cooler — a small slice of plausible deployments.

---

## 7. Floor Effects — Below what dBA does additional reduction stop being perceivable?

### 7.1 The room noise floor

A typical occupied room — even at 3 AM, in a quiet residential neighborhood — has an ambient floor of **18-22 dBA** from:

- HVAC standby (whoosh of ducts even when system idle): 5-15 dBA contribution.
- Refrigerator standby and cycling: 5-15 dBA at the desk in a typical apartment.
- Outdoor traffic infiltration through walls/windows: 5-15 dBA.
- Listener's own physiology (breathing, clothing rustle): 10-15 dBA.

In an open-plan office, the floor is 40-50 dBA. In a bedroom at night with closed window in a quiet suburb, the floor is 20-25 dBA. **A purpose-built sound-treated listening room** can hit ~15 dBA. Anything below ~15 dBA requires anechoic conditions.

### 7.2 The recommendation floor

Given:

1. JND for broadband noise is ~1 dB.
2. Detection threshold for fan-broadband against typical room ambient is approximately at the ambient level (50% detection at SNR ~0 dB).
3. Typical ambient is 18-25 dBA in residential settings.

**ventd should treat ~20 dBA at the listener as a "stop reducing" floor.** Below this:

- Fan reduction is below the user's room ambient, hence inaudible.
- Reduction is below or at the JND, so the controller is chasing changes the user cannot perceive.
- Fan duty is so low that thermal headroom is sacrificed for no perceptual gain.
- **Tonal residuals matter more than dBA.** A fan at 18 dBA broadband + a 30 dB SPL 80 Hz hum is worse than a fan at 25 dBA broadband with no hum. Reducing further chases the wrong metric.

This is consistent with SPCR's "silent PC = 15-20 dBA at 1 m" floor: they don't recommend chasing lower because in any normal room the difference becomes inaudible.

### 7.3 Concrete decision logic for the daemon

```
target_dBA = user_preset
ambient_estimate = max(measured_ambient, 20.0)   # never trust below 20 dBA
effective_floor = max(target_dBA, ambient_estimate - 3.0)
# stop reducing fan duty below effective_floor
# also: if predicted fan dBA - effective_floor < 1.0, do not change duty
```

The `ambient_estimate - 3 dB` rule reflects that fan noise 3 dB *below* ambient is at -3 dB SNR, comfortably below detection threshold.

---

## 7.4 Why "ambient minus 3 dB" is the right floor

The choice of `ambient - 3 dB` as the controller floor in §7.3 deserves a justification, because it has direct consequences for the daemon's behavior in quiet rooms.

The number comes from the DOSITS / Plomp detection-discrimination-recognition ladder (§3.1):

- At SNR = 0 dB (signal at masker level), detection probability is 50%.
- At SNR = -3 dB, detection probability drops below 25% for matched-spectrum maskers.
- At SNR = -6 dB, the signal is masked for nearly all listeners.

A fan running 3 dB below the room's ambient floor is detectable only ~25% of the time even by the most attentive listener. Going further is acoustically wasted effort (no perceptual gain), and physically risks under-cooling the system.

The choice not to go lower than `-3 dB` (as opposed to a more aggressive `-6 dB`) is also a hedge against measurement uncertainty in the ambient estimate. If our estimate of "ambient = 25 dBA" is actually 22 dBA in reality, the daemon at -3 dB would target 19 dBA, which is at the floor of plausible occupied-room ambient. Going to -6 dB would target 16 dBA — implausibly low and likely to result in fans stopping entirely or hunting against tach noise.

## 7.5 Hysteresis around the floor

A controller that strictly enforces `target_dBA <= cap` with a 1 dB JND gate will *hunt* — repeatedly reducing the fan, then re-increasing as temperature drifts up, then reducing again. Each transition is itself an audible event (Schultz, see §3.4 — ramps are more annoying than steady levels at the same L<sub>Aeq</sub>).

ventd should add hysteresis: once the daemon has reached the floor, it should hold the current fan duty until temperature drift requires a change of >2 dB worth of fan output, not 1 dB. This is consistent with the "loudness slew" recommendations in HVAC controls (ASHRAE Handbook ch. 49) which prefer step changes of <=3 dB and ramp times of >=10 seconds.

---

## 8. Frequency-Content Effects — Why dBA Alone is Insufficient

A 30 dBA broadband noise has its energy spread across the audio band (most of it 1-5 kHz where the ear is sensitive). A 30 dBA reading dominated by a single 100 Hz tone has, by virtue of A-weighting (A(100 Hz) = -19.1 dB), an *unweighted* SPL of ~49 dB at 100 Hz. That hum is unpleasant in a way the broadband hiss is not.

### 8.1 Recommended internal weighting

For ventd's internal control, in addition to dBA at the user's nominal listening position:

1. **Compute tone-to-noise ratio (TNR)** in 1/3-octave bands per ECMA-74 / ISO 7779. Flag any band with TNR > 5 dB as "tonal" and apply a 4-6 dB internal penalty against the user's budget.
2. **Compute low-frequency emphasis (LFE)** as L<sub>C</sub> - L<sub>A</sub>; values > 15 dB indicate the spectrum is low-frequency-dominated (per Persson & Björkman 1988, Industrial Noise & Vibration Centre guidance). Apply an additional 3-5 dB penalty if so.
3. **Display only dBA in the UI** because that's the user's mental model. Show a separate "tone-free quiet" indicator if both penalties are zero.

### 8.2 Why 30 dBA broadband != 30 dBA tonal

The user sets "32 dBA cap." Two control-policy outcomes both meet that cap:

- (A) Fans at 800 RPM: 28 dBA broadband, no tone. Total annoyance equivalent: ~28 dBA-broadband.
- (B) Fans at 1100 RPM in a structural resonance: 30 dBA broadband + a 4 dB peak at 110 Hz. Effective annoyance equivalent: ~35 dBA-broadband.

ventd should pick (A) even though both are "within budget." This is why the preset cap must be implemented as a multi-criterion gate, not a single threshold compared to a single dBA reading.

---

## 9. Recommended Preset Defaults — Shippable Table

The defaults below are the proposed ventd "quietness target" presets. They are calibrated to: the WHO/EPA standards in §2; SPCR and forum consensus in §5; the perceptual floor in §7; and the OEM precedents in §6. Each row gives a label, a numeric cap (the user-facing value, intended as dBA at the user's nominal listening position ~50 cm from the PC for a desktop, or laptop-deck for a laptop), the rationale, and what the daemon should *internally* be doing.

| Preset name | Cap (dBA at user) | When the user should pick this | Rationale / reference |
|---|---|---|---|
| **Inaudible (HTPC / studio)** | <=20 dBA | Shared bedroom HTPC, recording/mixing studio, sleep machine | Below typical room ambient. Per SPCR "silent PC" threshold. Daemon **must** drop to thermal-only floor and not pursue further reduction (§7). Likely unachievable on most desktops/laptops — UI should warn. |
| **Whisper (bedroom)** | <=25 dBA | Bedroom desktop, late-night work, stream-from-bed | At-or-below ambient in a quiet bedroom. WHO night-noise floor for sleep is 30 dBA *event*, so a 25 dBA continuous fan is comfortably sub-WHO. |
| **Office-quiet (recommended default)** | <=32 dBA | Typical day-work focus on a quiet home/private office | Aligns with EPA indoor-annoyance threshold (45 dBA) minus a generous margin and just below the community "32 dBA breakout" complaint level. Achievable on most modern hardware under typical workloads. **This should be the default preset out-of-box.** |
| **Office-pragmatic** | <=38 dBA | Open-plan office, daytime, machine on desk near user | Close to ASHRAE NC-35 office target. Fans will be audible but unobtrusive against typical office ambient (45 dBA). |
| **Performance** | <=45 dBA | Sustained-clock workloads, gaming, rendering, training | Matches EPA indoor activity-interference threshold; no quieter than a typical gaming laptop in performance mode. The daemon's job here is to **prevent runaway turbine-spinup**, not to be "quiet." |
| **Unmuzzled (no cap)** | n/a | Server-room, shop floor, deliberately running flat-out | Daemon does not throttle for noise; thermal/electrical envelope only. Ship as a checkbox, not a dBA value. |

Notes on UI:

- The default should be **Office-quiet (<=32 dBA)** because it matches the most common single number in community discussions, is just below WHO's bedroom number plus a typical-room ambient overhead, and corresponds to what the average desktop can achieve without exotic cooling.
- The free-form field should accept 18-50 dBA. Below 18, show an inline warning ("This is below typical room ambient — ventd cannot make your PC quieter than your room. Consider 25 dBA."). Above 50, ask the user if they meant a thermal cap instead.
- Each preset should have a tooltip with a real-world reference: "Whisper: like a quiet library", "Office-quiet: like a refrigerator across the room", "Performance: like a normal conversation in another room."

---

## 10. What Users Actually Type

Synthesized from forum threads (Overclock.net "Good dBA for a quiet computer", AnandTech "whisper-quiet" thread, Tom's Hardware multiple, SPCR forums "ambient noise measuring meaning"):

| Number typed | Frequency in discussions | Driving reasoning |
|---|---|---|
| 20 dBA | Rare | "True silence" believer; usually after reading SPCR. |
| 25 dBA | Common | Round number; "whispered conversation"; bedroom builders. |
| 27 dBA | Occasional | SPCR "quiet PC under load" threshold. |
| **30 dBA** | **Most common** | WHO bedroom; "quiet library"; default mental model. |
| 32 dBA | Specific | Heard the "breakout level" forum claim. |
| 35 dBA | Common | Daytime-only target; "I just don't want to hear it." |
| 40 dBA | Common | Refrigerator analogy; "I can live with that." |
| 45 dBA | Common | Performance gamer; "normal conversation level cap." |
| 50+ dBA | Uncommon as cap | Usually picked because the user has a loud baseline and just wants a *less-noisy* state. |

**Design decision:** the slider should snap to 1 dBA increments but visually mark 25/30/35/40/45 with named labels. The free-form text field should accept fractional values but round to integer for display.

---

## 11. Perceptual-Floor Analysis — When ventd Should Stop Reducing

A complete decision rule for the controller:

```python
def should_keep_reducing(current_predicted_dBA, target_dBA, ambient_dBA, tonal_penalty_dB, lfe_penalty_dB):
    # Effective broadband target after tonal/LFE accounting
    effective_target = target_dBA - tonal_penalty_dB - lfe_penalty_dB

    # Hard floor: never aim below ambient minus 3 dB (-3 dB SNR is sub-detection)
    hard_floor = max(effective_target, ambient_dBA - 3.0, 18.0)

    # JND gate: if a further reduction would change SPL by less than 1 dB, stop
    if (current_predicted_dBA - hard_floor) < 1.0:
        return False

    return current_predicted_dBA > hard_floor
```

The floor `18.0` is set conservatively below the lowest plausible occupied-room ambient (~20 dBA bedroom at night) because measurement uncertainty in ventd's ambient estimate is large; we don't want the daemon to refuse to spin down a fan because of a noisy ambient sample.

**Key invariant:** if `target_dBA < 20`, the daemon should warn the user once at preset-application time but still attempt to honor the spirit of the preset (maximally quiet) until the JND gate fires. Hiding the warning is bad UX; pretending the cap is achievable is worse.

---

## 11.1 Mic-vs-no-mic operating modes

ventd cannot, in v1, depend on a microphone being attached to the system. Almost no Linux server/workstation Linux deployment exposes a usable mic, and laptop mic input is gated by user permission and PulseAudio/PipeWire routing. That gives us three regimes to design for:

**Mode A — no mic, blind operation (default).** ventd has no measurement of ambient or fan output. It must:
- Assume ambient = 25 dBA (residential daytime median).
- Estimate fan dBA from a per-fan model: `dBA(rpm) = a + b * log10(rpm)` calibrated once at install time using a known reference (manufacturer spec at known RPM).
- Apply the §11 stop-reducing rule with conservative margins (treat the model as ±3 dB uncertain).
- Surface the preset cap to the user but admit in the UI that the cap is "estimated, not measured."

**Mode B — user-supplied ambient.** User types "my room is about 35 dBA" or picks a preset (bedroom / home-office / open-plan). ventd uses this as the ambient floor instead of the 25 dBA default. The §11 rule then becomes meaningful (no point reducing fan below user-stated ambient minus 3).

**Mode C — mic available.** ventd takes a 1 Hz L<sub>Aeq</sub> sample stream from the mic. Now ambient is measured continuously, fan level can be measured-vs-modelled (closing the model loop), and tonality can be detected via FFT. Mode C is the only mode where the daemon can deliver on a "32 dBA cap" with quantitative honesty.

**Recommendation:** ship Mode A at v1.0; surface a Mode B "describe your room" wizard at v1.1 (it costs a few hours of UI work, doubles the daemon's accuracy). Mode C is a v2 conversation.

---

## 12. User-Research Wishlist (HIL Data Only)

Things the literature does not answer for ventd specifically — these need actual deployments to settle:

1. **What is the distribution of ambient L<sub>Aeq</sub> in real ventd-user environments?** We have WHO/ASHRAE numbers for "typical bedroom" and "typical office," but the distribution among Linux desktop users (who skew toward home offices, basements, server-co-located workstations) is unknown. Implication: the daemon's default ambient assumption (currently 25 dBA) may be wrong by 10+ dB for a meaningful slice of users.
2. **How well does the user-supplied dBA cap correlate with measured cap once we have a microphone?** A user types "30 dBA"; in their actual room, what does ventd actually need to control to to make the user say "yes, that's quiet"? Subjective threshold may be 5-10 dB *higher* than typed value because users don't have an intuition for absolute SPL.
3. **Tonal sensitivity by user.** Some users report extreme annoyance at low-frequency hum even when L<sub>Aeq</sub> is 20 dBA; others don't notice 40 dB SPL bass tones. A simple psychoacoustic onboarding test (play 5 tones, ask "do you hear this?") could let the daemon set per-user tonal penalties. Worth piloting.
4. **Which RPM bands cause structural resonance on real chassis?** ventd can measure broadband fan noise but cannot easily measure chassis transfer functions. We would need either (a) a calibration sweep with mic input, or (b) a community-shared database keyed by case model. The latter is more achievable.
5. **Time-varying acceptability.** A constant 30 dBA may be acceptable; a fan ramping between 25 and 35 dBA may be more annoying than steady 35 dBA. Schultz-curve data is for L<sub>Aeq</sub> only; it does not capture ramp-rate-induced annoyance. ventd's controller likely needs a slew-rate penalty as well as a level cap.
6. **Cap-vs-budget framing.** Users ask for "<=32 dBA" but probably *want* "as quiet as you can without compromising thermals, with 32 dBA as a never-exceed cap." These are different control policies. We need user studies to confirm which mental model matches their language. Initial prior: cap-not-target.
7. **Does the user want presets, free-form, or both?** Hypothesis: most users will pick a preset and never touch it; power users want the slider. Confirm with telemetry on the first 1000 deployments.
8. **Floor-effect tolerance.** If ventd can achieve 22 dBA but the user typed 18, will the user be satisfied or angry? Hypothesis: a UI message ("Your room ambient is ~25 dBA; ventd is operating 3 dB below that, which is the practical floor") is better than silently failing the cap.

---

## 12.1 Acceptance criteria for the v1 preset feature

Concrete tests the preset must pass before it ships:

1. **Default preset is "Office-quiet (<=32 dBA)".** A user installing ventd with no configuration, on a typical desktop, should observe the daemon throttle fans during light load such that a sound-meter app (e.g. Decibel X on a phone, 50 cm away) reports <=35 dBA — accepting +3 dB measurement uncertainty.
2. **Whisper preset constraint.** On the "Whisper (<=25 dBA)" preset, the daemon must never command a fan duty that the calibrated model predicts will exceed 28 dBA at 1 m, except during a thermal-emergency override. The override must log a structured event with the temperature that triggered it.
3. **Preset switch latency.** Changing the preset must take effect within one control loop (≤2 s default), with the fan duty change ramping at ≤3 dB/s to avoid audible step transients (per §7.5 hysteresis logic).
4. **Floor-warning display.** If the user types a cap below their estimated ambient (Mode B) or below 20 dBA (Mode A), the UI must surface a warning before the preset is committed, with a one-click "I understand, set anyway" path. The warning must explain *why* the cap may not be perceptible.
5. **Tonal penalty enforcement (Mode C only).** When mic input is available, the daemon must measure tone-to-noise ratio per ECMA-74 in 1/3-octave bands and avoid RPM bands where TNR > 5 dB if doing so keeps the cap satisfiable.
6. **Telemetry.** The preset choice and any user free-form numeric value must be telemetered (with consent) so the v1.x cycle can refine defaults based on real distributions.

These six criteria are testable without HIL hardware (1, 3, 4 in unit/integration tests; 2 in calibrated thermal-bench tests; 5 only on Mode-C-capable boards; 6 in product analytics).

---

## 13. Citations

### Standards and authoritative bodies

1. **WHO Environmental Noise Guidelines for the European Region** (2018). World Health Organization. <https://www.who.int/europe/news-room/fact-sheets/item/noise>
2. **WHO Night Noise Guidelines for Europe** (2009). Berglund B, Lindvall T (eds.). <https://www.euro.who.int/__data/assets/pdf_file/0017/43316/E92845.pdf>
3. **WHO Guidelines for Community Noise** (1999). Berglund, Lindvall, Schwela. <https://docs.wind-watch.org/WHO-communitynoise.pdf> and chapter 4 summary at <https://www.ruidos.org/Noise/WHO_Noise_guidelines_4.html>
4. **EPA 550/9-74-004 "Levels Document"** (1974). *Information on Levels of Environmental Noise Requisite to Protect Public Health and Welfare with an Adequate Margin of Safety*. <https://www.nonoise.org/library/levels74/levels74.htm>; archived EPA summary at <https://www.epa.gov/archive/epa/aboutepa/epa-identifies-noise-levels-affecting-health-and-welfare.html>
5. **ISO 226:2023** Acoustics — Normal equal-loudness-level contours. <https://www.iso.org/standard/83117.html>; revision background at ResearchGate <https://www.researchgate.net/publication/374956941>
6. **ISO 1996-1:2016 / ISO 1996-2:2017** Acoustics — Description, measurement and assessment of environmental noise (and the tonal-penalty method).
7. **ASHRAE Handbook — HVAC Applications, ch. 49 "Noise and Vibration Control"** (2023). <https://handbook.ashrae.org/Handbooks/A23/IP/A23_Ch49/a23_ch49_ip.aspx>
8. **ANSI S12.2 / Noise Criterion (NC) curves**, summarized at <https://commercial-acoustics.com/guides/noise-criterion-nc-rating-101/> and <https://www.engineeringtoolbox.com/nc-noise-criterion-d_725.html>

### Peer-reviewed psychoacoustics

9. **Schultz TJ.** Synthesis of social surveys on noise annoyance. *J. Acoust. Soc. Am.* 64:377-405 (1978). <https://nwtteis.com/portals/nwtteis/files/references/Schultz_1978_Noise_Annoyance.pdf>
10. **Miedema HME, Vos H.** Exposure-response relationships for transportation noise. *J. Acoust. Soc. Am.* 104:3432-3445 (1998).
11. **Pedersen TH, Søndergaard LS et al.** Annoyance of low-level tonal sounds — A penalty model. *Applied Acoustics* (2018-19). <https://www.sciencedirect.com/science/article/abs/pii/S0003682X18307412>
12. **Lee J et al.** Subjective Evaluation on the Annoyance of Environmental Noise Containing Low-Frequency Tonal Components. *Int. J. Environ. Res. Public Health* 18(13):7127 (2021). <https://pmc.ncbi.nlm.nih.gov/articles/PMC8297235/>
13. **Jesteadt W, Wier CC, Green DM.** Intensity discrimination as a function of frequency and sensation level. *J. Acoust. Soc. Am.* 61:169-177 (1977). Summarized: <https://pubmed.ncbi.nlm.nih.gov/8445133/>
14. **Basner M et al.** Aircraft noise effects on sleep: mechanisms, mitigation and research needs (and the 6 dB-above-background awakening figure cited in WHO 2018 review). <https://pmc.ncbi.nlm.nih.gov/articles/PMC5877064/>
15. **Four decades of annoyance modelling** (review), *J. Acoust. Soc. Am.* (2025). <https://pubs.aip.org/asa/jasa/article/158/1/R1/3351269/Four-decades-of-annoyance-modelling>

### Industry and community references

16. **Silent PC Review** — *Introduction to Recommended Silent / Quiet Components*. <https://silentpcreview.com/introduction-to-recommended-silent-quiet-components/> (15-20 dBA "silent" / 20-27 dBA "quiet" thresholds.)
17. **Silent PC Review** — *A Primer on Noise in Computing* (archived). <http://www.silentpcreview.com/Primer_on_Computer_Noise>
18. **Noctua NF-A12x25 PWM specifications**. <https://www.noctua.at/en/products/nf-a12x25-pwm/specifications>
19. **be quiet! Silent Wings 4** product page. <https://www.bequiet.com/en/casefans/silent-wings-4/3696>
20. **Industrial Noise & Vibration Centre** — *Why dB(A) in regulations and ordinances fails to assess low frequency hum complaints*. <https://invc.com/noise-control/tonalysis-adding-simple-low-frequency-tonal-analysis-to-dba/>
21. **NotebookCheck** — *How does Notebookcheck test laptops*. <https://www.notebookcheck.net/How-does-Notebookcheck-test-laptops-and-smartphones-A-behind-the-scenes-look-into-our-review-process.15394.0.html>
22. **AnandTech forum**, "At what dBA rating is a fan considered whisper-quiet?". <https://forums.anandtech.com/threads/at-what-dba-rating-is-a-fan-considered-whisper-quiet.886574/>
23. **Overclock.net forums** — "Good dBA for a quiet computer?" and "At what dBA would a fan become audible?". <https://www.overclock.net/threads/good-dba-for-a-quiet-computer.469665/>; <https://www.overclock.net/threads/at-what-dba-would-a-fan-become-audible.496542/>
24. **Tom's Hardware forum**, "Is a 32 decibel case fan loud?". <https://forums.tomshardware.com/threads/is-a-32-decibel-case-fan-loud.2845982/>
25. **macperformanceguide.com**, *2023 MacBook Pro M3 Max: Intolerably Loud Fan Noise*. <https://macperformanceguide.com/MacBookPro2023-Acoustics.html>
26. **HotHardware**, *Dell XPS 13 9315 Laptop Review* (acoustics page). <https://hothardware.com/reviews/dell-xps-13-9315-review?page=3>
27. **UltrabookReview**, *Optimized Quiet gaming at 35 dBA on the Asus ROG Zephyrus G14*. <https://www.ultrabookreview.com/68002-zephyrus-g14-quiet-gaming/>
28. **Soundtrace**, *Data Center Noise Levels and Hearing Conservation*. <https://www.soundtrace.com/blog/data-center-noise-levels-hearing-conservation-osha-compliance>
29. **DOSITS / Discovery of Sound in the Sea**, *Detection threshold* (detection/discrimination/recognition SNR ladder). <https://dosits.org/animals/advanced-topics-animals/detection-threshold/>
30. **ABD Engineering & Design**, *Annoyance from Tonal vs. Broadband Sounds*. <https://www.abdengineering.com/blog/psychoacoustics-tonal-broadband-sounds/>

### Reference and education

31. **Wikipedia**, *Equal-loudness contour*. <https://en.wikipedia.org/wiki/Equal-loudness_contour>
32. **Wikipedia**, *Just-noticeable difference*. <https://en.wikipedia.org/wiki/Just-noticeable_difference>
33. **Wikipedia**, *Weber-Fechner law*. <https://en.wikipedia.org/wiki/Weber%E2%80%93Fechner_law>
34. **Wikipedia**, *Cocktail party effect*. <https://en.wikipedia.org/wiki/Cocktail_party_effect>
35. **Yale EHS**, *Decibel level comparison chart*. <https://ehs.yale.edu/sites/default/files/files/decibel-level-chart.pdf>
36. **Noise Awareness Day**, *Common Noise Levels*. <https://noiseawareness.org/info-center/common-noise-levels/>
37. **sengpielaudio**, *Loudness volume doubling sound level change*. <https://sengpielaudio.com/calculator-levelchange.htm>

---

## 14. Open Questions for Ventd Spec Authors

These are the explicit decisions the spec must make, given this research:

- Is the cap a **hard never-exceed** (controller refuses to cross it even at thermal cost) or a **soft target** (controller exceeds briefly during transients)? Recommendation: hard cap with documented thermal-emergency override only.
- Should the daemon **measure** ambient via mic input (requires a sensor we don't currently have) or **estimate** via heuristics (always assume >=20 dBA)? Recommendation: estimate at v1, accept user-provided ambient at v1.5, accept mic input at v2 if hardware exists.
- Where is the listener position? Is a desktop user at 50 cm from the chassis, or 1 m? Most user types implicit "at my ears, while I sit at my desk." Default to 50 cm for desktop, 30 cm for laptop deck, configurable.
- Does the dBA target travel with the user across machines, or is it per-machine? Recommendation: per-machine, because thermal envelopes differ.
- How does the cap interact with **smart-mode** (R5/R7) workload signatures? A workload that needs sustained 65 W can't run under a 25 dBA cap on a 100 W cooler. The spec must define what wins: cap, thermal, or workload completion. Recommendation: cap wins; thermal-throttle wins over the cap only when the chip would otherwise damage itself.

---

## 15. Cross-references within the ventd research bundle

- **R5 (defining-idle multi-signal predicate)** and **R7 (workload signature hash)** provide the workload-classification machinery that the noise cap must coexist with. The §14 open question on cap-vs-thermal-vs-workload precedence is unresolved at R32; the spec authors must close it.
- **R11 (sensor noise floor thresholds)** uses the same JND-style logic for tach signal noise. The acoustic floor analysis here in §7 mirrors R11's threshold logic but at the perceptual rather than the electrical layer. Both should remain consistent: ventd does not chase signals it cannot measure or perceptions it cannot deliver.
- **R28 (fan-control failure modes)** documents the chassis/firmware-specific cases where the daemon may not be able to honor a quietness target at all (e.g. firmware overrides, hardwired curves, EC mode-locks). The preset UI must surface "your hardware does not support ventd quietness control" gracefully; the dBA cap is meaningless in those cases.
- **calibration-time-budget.md** sets the time the daemon has to learn a fan model before applying user presets. A noise cap is unenforceable until the per-fan model is calibrated to ±3 dB; the preset UI must wait for calibration to complete before claiming "32 dBA cap active."

*End R32. Bundle this with R28 (failure modes) and R11 (sensor noise floor) when wiring the preset UI; the floor analysis here mirrors R11's logic but at the perceptual layer.*
