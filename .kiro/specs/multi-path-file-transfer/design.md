# Design Document - Multi-Path File Transfer

## Overview

Le système de transfert de fichier multi-path utilise l'infrastructure KWIK existante pour permettre le téléchargement parallèle de fichiers via plusieurs chemins. Le serveur principal coordonne avec le serveur secondaire pour distribuer les chunks de fichier, optimisant ainsi la bande passante totale disponible.

## Architecture

### Composants principaux

1. **FileTransferClient** - Interface client pour les téléchargements
2. **FileTransferServer** - Gestionnaire de fichiers sur le serveur principal  
3. **ChunkCoordinator** - Coordinateur de distribution des chunks
4. **ChunkManager** - Gestionnaire de chunks côté client
5. **FileChunk** - Structure de données pour les portions de fichier

### Flux de données

```
Client                 Serveur Principal              Serveur Secondaire
  |                           |                              |
  |-- DownloadFile(name) ---->|                              |
  |                           |-- Analyse fichier ---------->|
  |                           |-- Calcul chunks ------------>|
  |                           |                              |
  |<-- Chunk 1,3,5 ----------|                              |
  |                           |-- SEND_CHUNK(2,4,6) ------->|
  |<-- Chunk 2,4,6 -----------------------------------|     |
  |                           |                              |
  |-- Reconstitution -------->|                              |
```

## Components and Interfaces

### FileTransferClient Interface

```go
type FileTransferClient interface {
    DownloadFile(filename string, progressCallback func(progress float64)) error
    GetDownloadProgress(filename string) (*TransferProgress, error)
    CancelDownload(filename string) error
}

type TransferProgress struct {
    Filename        string
    TotalChunks     int
    ReceivedChunks  int
    TotalBytes      int64
    ReceivedBytes   int64
    Speed           float64 // bytes/sec
    ETA             time.Duration
}
```

### FileChunk Structure

```go
type FileChunk struct {
    ChunkID      uint32
    SequenceNum  uint32
    Data         []byte
    Checksum     string
    IsLast       bool
    TotalChunks  uint32
    Filename     string
}

type ChunkCommand struct {
    Command      string // "SEND_CHUNK", "CHUNK_SENT", "CHUNK_ERROR"
    ChunkID      uint32
    Filename     string
    StartOffset  int64
    Size         int32
    Checksum     string
    Metadata     map[string]string
}
```

### ChunkCoordinator

```go
type ChunkCoordinator struct {
    primaryServer   *ServerSession
    secondaryPaths  []string
    chunkSize       int32
    activeTransfers map[string]*TransferState
}

type TransferState struct {
    Filename        string
    TotalChunks     uint32
    PrimaryChunks   []uint32
    SecondaryChunks []uint32
    CompletedChunks map[uint32]bool
    StartTime       time.Time
}
```

## Data Models

### Chunk Distribution Strategy

Le système utilise une stratégie de distribution alternée par défaut :
- Chunks impairs (1,3,5...) → Serveur Principal
- Chunks pairs (2,4,6...) → Serveur Secondaire

Cette stratégie peut être optimisée dynamiquement selon :
- La bande passante mesurée de chaque chemin
- La latence de chaque serveur
- La charge actuelle des serveurs

### File Metadata

```go
type FileMetadata struct {
    Filename     string
    Size         int64
    ChunkSize    int32
    TotalChunks  uint32
    Checksum     string
    CreatedAt    time.Time
    ModifiedAt   time.Time
}
```

## Error Handling

### Types d'erreurs

1. **ChunkTimeoutError** - Chunk non reçu dans le délai
2. **ChunkCorruptionError** - Checksum invalide
3. **ServerUnavailableError** - Serveur ne répond pas
4. **FileNotFoundError** - Fichier demandé inexistant
5. **InsufficientSpaceError** - Espace disque insuffisant

### Stratégies de récupération

1. **Retry automatique** - 3 tentatives par chunk
2. **Fallback serveur** - Basculement vers l'autre serveur
3. **Reconstruction partielle** - Sauvegarde des chunks reçus
4. **Reprise de transfert** - Continuation depuis le dernier chunk reçu

## Testing Strategy

### Tests unitaires

1. **ChunkManager** - Création, validation, reconstruction
2. **FileTransferClient** - Interface et callbacks
3. **ChunkCoordinator** - Distribution et coordination
4. **Checksum validation** - Intégrité des données

### Tests d'intégration

1. **Transfert simple** - Fichier petit (< 1MB)
2. **Transfert multi-chunk** - Fichier moyen (10-100MB)
3. **Transfert avec erreurs** - Simulation de pannes
4. **Transfert concurrent** - Plusieurs fichiers simultanés

### Tests de performance

1. **Bande passante** - Mesure du débit total
2. **Latence** - Temps de réponse des chunks
3. **Efficacité** - Comparaison mono vs multi-path
4. **Scalabilité** - Performance avec fichiers volumineux

## Implementation Notes

### Intégration avec KWIK existant

Le système utilise les APIs KWIK existantes :
- `SendRawData()` pour les commandes de chunks
- `AcceptStream()` pour recevoir les chunks
- Path management pour la distribution

### Optimisations prévues

1. **Compression** - Compression des chunks avant envoi
2. **Mise en cache** - Cache des chunks fréquemment demandés
3. **Prédiction** - Pré-chargement des chunks suivants
4. **Adaptation** - Ajustement dynamique de la taille des chunks

### Sécurité

1. **Validation des noms** - Prévention des attaques de traversée de répertoire
2. **Limitation de taille** - Limite sur la taille des fichiers
3. **Authentification** - Vérification des droits d'accès
4. **Chiffrement** - Protection des données en transit (via QUIC/TLS)