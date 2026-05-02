# Chippy Mascot — Image Generation Prompts

Saved prompts for generating the chippy project mascot ("Chippy" the
anthropomorphized 6502 DIP chip). See "Concept" section below for the
character spec, then pick a variant prompt to feed into your image generator
of choice (Midjourney, ChatGPT GPT-Image-1, Gemini Imagen 3, Bing Image
Creator, Stable Diffusion, Flux, etc).

---

## Concept

**Chippy** is the project mascot — a friendly anthropomorphized MOS 6502
40-pin DIP integrated circuit. The visual language sits at the intersection
of *retro hardware nostalgia* and *approachable terminal tool*, in the same
spirit as the Go gopher and Rust's Ferris.

### Character spec (lock these traits across every variant)

| Attribute | Spec |
|---|---|
| Body | Black ceramic 40-pin DIP IC, rectangular, slightly rounded corners |
| Top of body | Small semicircular notch (the chip's orientation indicator) — leave visible above the eyes like a tiny hat brim |
| Markings | "MOS 6502" or "6502" in white silkscreen on the body, small dot in upper-left corner (pin 1 indicator) |
| Eyes | Two large round black expressive eyes with single white highlight, positioned in the upper third of the body |
| Mouth | Small simple smile, just below the eyes |
| Pins | Silver/chrome metallic pins on both long edges; the bottom row pins curve forward as little feet, two upper pins extend as arms with rounded "hand" tips |
| Color palette | Black body, silver pins, white text, single accent color (CRT amber `#ffb000` OR phosphor green `#33ff33` — pick one and keep it consistent across all variants) |
| Style | Flat vector illustration, clean thick outlines, soft cel-shaded highlights, minimal gradients, friendly Saturday-morning-cartoon energy. Reference the visual language of the Go gopher and Ferris the crab. |
| Background | Solid pastel or transparent — never busy |
| Negative | no text artifacts, no extra limbs, no realistic photorealism, no horror, no metallic chrome over-rendering, no random circuit board chaos |

---

## Variant 1 — Hero pose (README header / social preview)

> Mascot character "Chippy": a cute anthropomorphized 40-pin DIP integrated
> circuit chip with a black ceramic body, "MOS 6502" printed in white on its
> body, small semicircular notch on top, large round expressive black eyes
> with white highlights, friendly smile, silver metallic pins curving down
> as little legs and out as arms, standing upright in a hero pose with arms
> slightly raised, soft amber CRT-glow rim light on the body, flat vector
> illustration, clean thick black outlines, cel-shaded, Saturday morning
> cartoon style similar to the Go gopher or Rust Ferris, soft cream pastel
> background, friendly approachable energy, mascot illustration,
> sticker-ready. Square 1:1 composition, character centered, full body
> visible.

---

## Variant 2 — Avatar / favicon (GitHub org avatar, 16×16 viable)

> Mascot character "Chippy" the 6502 chip in a tight head-and-shoulders
> portrait, cropped just below the upper pins, large expressive eyes,
> friendly smile, "6502" visible in white text on the body, black ceramic
> surface with subtle highlight, silver pin tips visible at the edges, flat
> vector illustration style, bold thick outlines (must remain readable when
> scaled to 16×16 pixels), single-color background (pastel cream or soft
> amber), no fine details that disappear at small sizes, sticker logo
> style, mascot avatar.

---

## Variant 3 — Debugger Chippy (holding a breakpoint flag)

> Mascot character "Chippy" the 6502 chip standing upright, holding a small
> red flag in one pin-arm with a white circle on it (a debug breakpoint
> marker), the other pin-arm raised in a thumbs-up, friendly mischievous
> expression with one eye slightly winking, body labeled "MOS 6502" in
> white, silver pins as limbs, soft phosphor-green glow around the flag,
> flat vector illustration, clean cel-shaded, Saturday morning cartoon
> style, soft pastel background, mascot sticker style.

---

## Variant 4 — Coffee Chippy (Buy Me a Coffee tie-in)

> Mascot character "Chippy" the 6502 chip sitting cross-legged on the
> floor, holding a steaming cup of coffee in both pin-hands, contented
> closed-eyes smile, small heart or steam wisps rising from the cup, body
> labeled "MOS 6502" in white, silver pins as limbs, warm amber CRT-glow
> lighting, flat vector illustration, cel-shaded, friendly cozy energy,
> soft cream pastel background, mascot sticker style. Reference the visual
> language of the Go gopher mascot.

---

## Variant 5 — At the terminal (full scene, for blog posts / docs hero)

> Mascot character "Chippy" the 6502 DIP chip sitting at a tiny vintage CRT
> terminal, pin-hands resting on a small mechanical keyboard, the
> green-phosphor screen showing a hex dump and a "> _" prompt, surrounded
> by a cozy retro-computing desk scene with a coffee mug and a stack of
> floppy disks, warm desk lamp lighting, flat vector illustration with cel
> shading, Saturday morning cartoon style, friendly nostalgic 1980s
> home-computer vibe, soft pastel color palette, character clearly the
> focal point, scene composed in 16:9 widescreen.

---

## Workflow tips

- **Generate Variant 1 first.** Once you have a Chippy you like, screenshot
  it and feed it back into the generator as a reference image (Midjourney
  `--cref`, ChatGPT "use this character", Gemini Nano-Banana edit) for the
  other variants. This is the only way to get character consistency across
  images.
- **Pick amber OR green, not both.** Mixing CRT colors makes the character
  feel inconsistent across merch.
- **If the generator keeps mangling "MOS 6502" text**, either (a) leave it
  off and add the text in a vector editor afterward, or (b) reduce to just
  "6502" which AI handles better.
- **For favicon use**, run the final hero through a favicon generator (e.g.
  realfavicongenerator.net) — it'll preview how it degrades at 16/32/180px.

## Generator recommendations (ranked for this use case)

1. **ChatGPT (Plus, $20/mo)** — best in-context character consistency.
   Generate Variant 1, then in the same chat say "now Chippy holding
   coffee" — it remembers the character.
2. **Midjourney ($10/mo)** — best raw quality. Use `--cref` for character
   consistency.
3. **Google Gemini / Imagen 3 (free tier)** — best free option with quality.
4. **Bing Image Creator (free)** — uses GPT-Image-1 under the hood, free
   with Microsoft account, daily quota.
5. **Stable Diffusion / Flux** via mage.space, leonardo.ai, or fal.ai —
   open-model fallback, free or pay-per-image.
