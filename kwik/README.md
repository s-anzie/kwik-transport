# KWIK - QUIC With Intelligent Konnections

KWIK est un protocole de transport basé sur QUIC-go qui permet à un client de communiquer simultanément avec plusieurs serveurs de manière transparente. Le protocole maintient une interface identique à QUIC tout en gérant en interne un agrégat complexe de connexions QUIC distinctes avec des plans de données et de contrôle sophistiqués.

## Structure du Projet

```
kwik/
├── pkg/                   # Packages publics
│   ├── session/           # Gestion des sessions KWIK
│   ├── stream/            # Gestion des flux KWIK
│   ├── transport/         # Couche transport
│   ├── protocol/          # Définitions du protocole
│   └── control/           # Plan de contrôle
├── proto/                 # Définitions protobuf
├── internal/              # Implémentations internes
│   └── utils/             # Utilitaires
└── examples/              # Exemples d'utilisation
```

## Fonctionnalités Principales

- **Interface Compatible QUIC** : Utilise les mêmes méthodes que QUIC (Dial, Listen, OpenStreamSync, AcceptStream)
- **Multi-connectivité** : Connexions simultanées vers plusieurs serveurs
- **Plans Séparés** : Séparation entre plan de contrôle et plan de données
- **Optimisation des Flux** : Multiplexage intelligent des flux logiques
- **Transmission de Paquets Bruts** : Support pour protocoles personnalisés

## Installation

```bash
go mod tidy
```

## Utilisation de Base

```go
// Client
session, err := kwik.Dial(context.Background(), "server:4433")
if err != nil {
    log.Fatal(err)
}

stream, err := session.OpenStreamSync(context.Background())
if err != nil {
    log.Fatal(err)
}

// Utilisation identique à QUIC
_, err = stream.Write([]byte("Hello KWIK"))
if err != nil {
    log.Fatal(err)
}
```

## Développement

Ce projet est en cours de développement. Consultez les spécifications dans `.kiro/specs/kwik-transport-protocol/` pour plus de détails sur l'architecture et les exigences.