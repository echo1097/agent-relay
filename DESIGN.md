---
name: Agent Relay
description: A quiet desktop workspace for agent conversations.
colors:
  background: "#171a18"
  panel: "#1d211e"
  sidebar: "#121513"
  border: "#333b35"
  text: "#edf0e9"
  muted: "#a5afa5"
  accent: "#c5dc9c"
  selected: "#2b3528"
  hover: "#242b25"
typography:
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "13px"
    lineHeight: 1.75
  heading:
    fontSize: "26px"
    fontWeight: 600
    letterSpacing: "-0.025em"
  title:
    fontSize: "20px"
    fontWeight: 600
    lineHeight: 1.45
  label:
    fontSize: "12px"
rounded:
  control: "6px"
  input: "7px"
spacing:
  compact: "12px"
  regular: "18px"
  generous: "28px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "#1c2519"
    rounded: "{rounded.control}"
    padding: "9px 13px"
---

## Overview

**Creative North Star: "Conversation first"**

Agent Relay is a desktop work tool. Flat, quiet surfaces put conversations and their controls ahead of decoration. The established green brand accent stays restrained, with clearer neutral surfaces and readable text.

## Colors

Use the accent for primary actions, selected navigation, unread indicators, and keyboard focus. Neutral layers separate navigation, lists, and reading panes. Amber identifies questions needing replies; muted text remains readable.

## Typography

Use the platform sans throughout. Hierarchy comes from weight, spacing, and a compact type scale. Dates use tabular figures. Long messages preserve whitespace and wrap without widening the pane.

## Layout

The desktop shell fills the window. The sidebar is 194px, reducing to 168px below 1120px. Inbox uses a 365px list, reducing to 310px. Lists and conversation bodies scroll independently, keeping toolbars and the composer visible. The app supports windows down to 960 by 640px.

**The Centered Controls Rule.** Center complete filter groups using flex layout, never positional offsets.

## Elevation & Depth

Use flat surfaces and single borders. Only transient feedback uses a soft shadow. Avoid nested card containers.

## Shapes

Controls use modest rounded corners. Provider logos use equal visible dimensions, compensating for transparent image margins.

## Components

The conversation list shows the local agent, date, subject, preview, and counterpart. The reading pane provides previous/next navigation, copy, export, and disclosure of detailed metadata. The composer shows its sending identity and delivery state. Navigation and filters have explicit selected states. Use matching outline SVG icons and labeled icon buttons.

Preserve focus, drafts, and scroll positions during background refreshes. Animate only brief feedback, honoring reduced motion.

## Do's and Don'ts

- Do show real saved history and honest delivery status.
- Do keep connection details accessible through the sidebar status disclosure.
- Do use supplied provider images.
- Don't mark agent messages read just because a person viewed them.
- Don't add decorative dashboard metrics or repeat the same activity in multiple panels.
- Don't add blue focus effects.
