---
title: neomd
layout: hextra-home
toc: false
---

<div class="hx-mt-6 hx-mb-6">
{{< hextra/hero-headline >}}
  A minimal terminal email client&nbsp;<br class="sm:hx-block hx-hidden" />for people who read & write in Markdown
{{< /hextra/hero-headline >}}
</div>

<div class="hx-mb-12">
{{< hextra/hero-subtitle >}}
  Compose in Neovim, navigate with Vim motions, screen emails like HEY,&nbsp;<br class="sm:hx-block hx-hidden" />process your inbox with GTD — all from the terminal
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx-mb-6">
{{< hextra/hero-button text="Overview and Philosophy" link="docs/overview" >}}
</div>

<div class="hx-mt-6"></div>

<div class="hx-mt-12 hx-mb-8">
<h2 class="hx-text-4xl hx-font-bold hx-tracking-tight hx-text-gray-900 dark:hx-text-gray-50">What Makes neomd Different?</h2>
</div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="HEY-Style Screener"
    subtitle="Unknown senders wait in ToScreen until you approve (I), block (O), or categorize them. You choose who reaches your inbox — bye-bye spam."
    class="aspect-auto md:aspect-[1.1/1] max-md:min-h-[340px]"
    style="background: radial-gradient(ellipse at 50% 80%,rgba(1,97,254,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="GTD Workflow"
    subtitle="Process your inbox only once. Move emails to Waiting, Someday, Scheduled, or Archive with single keystrokes. Includes Feed and PaperTrail for newsletters and receipts."
    class="aspect-auto md:aspect-[1.1/1] max-lg:min-h-[340px]"
    style="background: radial-gradient(ellipse at 50% 80%,rgba(194,97,254,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="Superhuman Speed"
    subtitle="Folder switches in ~33ms (on fast IMAP providers like Hostpoint). Every action is instant — no loading spinners, no delays. Navigate with Vim motions."
    class="aspect-auto md:aspect-[1.1/1] max-md:min-h-[340px]"
    style="background: radial-gradient(ellipse at 50% 80%,rgba(142,53,74,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="Neovim Integration"
    subtitle="Compose in $EDITOR (nvim), send as Markdown → HTML multipart. Pre-send review prevents accidental sends. Auto-backup drafts to ~/.cache."
    class="aspect-auto md:aspect-[1.1/1] max-md:min-h-[340px]"
    style="background: radial-gradient(ellipse at 50% 80%,rgba(12,53,74,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="Direct IMAP/SMTP"
    subtitle="No local sync daemon. Uses RFC 6851 MOVE for instant operations. Works on any device with your mailbox always in sync."
    class="aspect-auto md:aspect-[1.1/1] max-lg:min-h-[340px]"
    style="background: radial-gradient(ellipse at 50% 80%,rgba(221,210,59,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="Keyboard-First"
    subtitle="Vim motions everywhere. j/k navigation, gg/G jumps, / search, numbered links [1]-[0], multi-select with m, undo with u."
    class="aspect-auto md:aspect-[1.1/1] max-md:min-h-[340px]"
    style="background: radial-gradient(ellipse at 50% 80%,rgba(59,130,246,0.15),hsla(0,0%,100%,0));"
  >}}
{{< /hextra/feature-grid >}}

<br>

<div class="hx-mt-12 hx-mb-8">
<h2 class="hx-text-4xl hx-font-bold hx-tracking-tight hx-text-gray-900 dark:hx-text-gray-50">Video Demo</h2>
</div>

YouTube rundown of most features:
[![neomd demo](https://img.youtube.com/vi/lpmHqIrCC-w/maxresdefault.jpg)](https://youtu.be/8aKkldYLWV8)


<div class="hx-mt-12 hx-mb-8">
<h2 class="hx-text-4xl hx-font-bold hx-tracking-tight hx-text-gray-900 dark:hx-text-gray-50">Documentation</h2>
</div>

{{< cards cols="3" >}}
  {{< card link="docs/overview" title="Overview & Philosophy" subtitle="Full feature list, installation (binary, AUR, source), philosophy, benchmarks, and inspiration" >}}
  {{< card link="docs/configuration" title="Configuration Reference" subtitle="Full config with multiple accounts, OAuth2, signatures, and UI options" >}}
  {{< card link="docs/keybindings" title="Keybindings" subtitle="Complete keyboard shortcuts reference (auto-generated from source)" >}}
  {{< card link="docs/screener" title="Screener Workflow" subtitle="How to classify emails, bulk operations, and screener lists" >}}
  {{< card link="docs/reading" title="Reading Emails" subtitle="Navigation, images, links, attachments, threading" >}}
  {{< card link="docs/sending" title="Sending Emails" subtitle="Compose, attachments, CC/BCC, drafts, HTML signatures" >}}
{{< /cards >}}

<br>

<div class="hx-mb-6">
{{< hextra/hero-button text="Getting Started: Install" link="docs/overview#install" >}}
</div>


<div class="hx-mt-12 hx-mb-8">
<h2 class="hx-text-4xl hx-font-bold hx-tracking-tight hx-text-gray-900 dark:hx-text-gray-50">Links</h2>
</div>

- [GitHub Repository](https://github.com/ssp-data/neomd)
- [Changelog](https://github.com/ssp-data/neomd/blob/main/CHANGELOG.md)
- [Roadmap](https://www.ssp.sh/brain/neomd#roadmap)
- [Security Policy](https://github.com/ssp-data/neomd/blob/main/SECURITY.md)
