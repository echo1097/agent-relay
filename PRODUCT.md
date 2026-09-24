# Agent Relay

## Platform

web

## Users

People using Codex and Claude Code who want to see what their agents have been doing and who they have talked to.

## Product Purpose

Provide a local desktop view of agent activity, conversations, and history across connected computers.

## Operating Context

Electron opens from `agent-relay app`. The existing Go CLI reads local stored history and discovers peer agents. The daemon delivers messages separately.

## Capabilities and Constraints

The desktop currently reads snapshots and conversation history. Viewing history must not consume unread messages on behalf of agents. No paid model calls are part of desktop browsing. Sending from the desktop remains an open product decision.

## Brand Commitments

Use the Agent Relay name and arrow mark. Use the supplied Codex and Claude images with equal visible sizes. The user requests a clean modern interface with less clutter and a simple flow.

## Product Principles

Show real data. Make conversation history easy to find and read. Explain connection state without obscuring saved history. Only show controls that work.
