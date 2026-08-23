# Playlist Synchronization

The core domain for bidirectionally synchronizing, reconciling, and auditing musical playlists between heterogeneous digital streaming platforms.

## Language

### Core Entities

**Playlist**:
An ordered collection of musical tracks created and curated on a streaming platform.
_Avoid_: Album, folder, mixtape, channel

**Track**:
An individual recorded piece of music characterized by its title, contributing artists, album, and playback duration.
_Avoid_: Song, audio file, video, item, media

**Source Platform**:
The music service hosting the reference playlist and track metadata from which synchronization originates.
_Avoid_: Upstream, origin, input provider

**Target Platform**:
The music service hosting the destination playlist where tracks are discovered, added, or reconciled.
_Avoid_: Downstream, destination, sink

### Matching & Resolution

**Matching Candidate**:
A track discovered on the target platform that potentially corresponds to a track on the source platform.
_Avoid_: Search hit, query result, candidate item

**Confidence Score**:
A quantified metric reflecting the certainty that a matching candidate corresponds to a source track.
_Avoid_: Match rate, similarity weight, accuracy rating

### Reconciliation & State

**Differential Reconciliation**:
The state comparison between source and target playlists that determines additions, retentions, and removals required for convergence.
_Avoid_: Full overwrite, sync pass, batch update

**Extraneous Track**:
A track present in the target playlist that has no corresponding counterpart in the source playlist.
_Avoid_: Orphan track, dirty track, extra item, unwanted song

### Authentication & Storage

**Session Credential**:
An authenticated identity token or session state authorizing operational access to a platform user library.
_Avoid_: API key, auth secret, password

**Migration Artifact**:
A structured record capturing snapshots, reconciliation plans, or execution outcomes of a synchronization run.
_Avoid_: Dump file, export log, output payload

### Integrity & Assurance

**Invariant Assertions**:
Formal domain and cardinality rules that must hold true across all stages of synchronization to guarantee complete data consistency.
_Avoid_: Sanity checks, validation rules, health probes
