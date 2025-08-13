# Requirements Document

## Introduction

Cette spécification définit la modification de l'architecture KWIK pour isoler les ouvertures de streams des serveurs secondaires. Actuellement, les serveurs secondaires peuvent ouvrir des streams directement sur la session cliente publique, comme le serveur principal. Cette fonctionnalité doit être restreinte : seul le serveur principal doit pouvoir ouvrir des streams sur la session cliente publique, tandis que les serveurs secondaires doivent gérer leurs streams en interne avec une agrégation transparente côté client.

## Requirements

### Requirement 1 - Restriction des Ouvertures de Streams

**User Story:** En tant que système KWIK, je veux que seul le serveur principal puisse ouvrir des streams directement sur la session cliente publique pour que l'architecture soit plus cohérente et contrôlée.

#### Acceptance Criteria

1. WHEN le serveur principal appelle OpenStreamSync() THEN le système SHALL créer un stream directement sur la session cliente publique
2. WHEN un serveur secondaire appelle OpenStreamSync() THEN le système SHALL créer un stream uniquement sur sa connexion QUIC interne
3. WHEN un serveur secondaire tente d'ouvrir un stream THEN le stream ne SHALL pas apparaître directement dans la session cliente publique
4. WHEN le client appelle AcceptStream() THEN il SHALL recevoir uniquement les streams ouverts par le serveur principal
5. WHEN des streams sont ouverts par des serveurs secondaires THEN ils SHALL être gérés entièrement en interne par KWIK

### Requirement 2 - Gestion Interne des Streams Secondaires

**User Story:** En tant que client KWIK, je veux que les streams des serveurs secondaires soient gérés en interne pour que la gestion des chemins reste transparente sans exposer la complexité multi-serveurs.

#### Acceptance Criteria

1. WHEN un serveur secondaire ouvre un stream sur sa session kwik et que ça passe sur sa connexion QUIC THEN le client SHALL recevoir ce stream en interne via la connexion QUIC du chemin
2. WHEN un stream secondaire est reçu côté client THEN il SHALL être traité par les composants internes de KWIK sans exposition publique
3. WHEN des données arrivent sur un stream secondaire THEN elles SHALL être agrégées dans les streams KWIK appropriés
4. WHEN un stream secondaire est fermé THEN la fermeture SHALL être gérée en interne sans affecter l'interface publique
5. WHEN des streams secondaires sont actifs THEN leur existence SHALL être transparente pour l'application cliente

### Requirement 3 - Agrégation des Données des Streams Secondaires

**User Story:** En tant que système KWIK, je veux agréger les données des streams secondaires dans les streams KWIK appropriés pour que les données soient positionnées au bon offset dans le bon flux.

#### Acceptance Criteria

1. WHEN des données sont écrites par un serveur secondaire dans son stream THEN elles SHALL être identifiées avec l'ID du stream KWIK cible et l'offset approprié
2. WHEN des données de streams secondaires sont reçues THEN elles SHALL être positionnées dans le stream KWIK correspondant selon leur offset
3. WHEN plusieurs serveurs secondaires écrivent dans le même stream logique THEN les données SHALL être agrégées de manière cohérente
4. WHEN des données sont agrégées THEN l'ordre et l'intégrité SHALL être préservés selon les offsets KWIK
5. WHEN l'agrégation est effectuée THEN elle SHALL être transparente pour l'application qui lit le stream KWIK

### Requirement 4 - Communication QUIC Interne

**User Story:** En tant que système KWIK, je veux une logique de communication QUIC robuste entre les éléments internes côté client et serveurs secondaires pour que l'agrégation des streams soit efficace.

#### Acceptance Criteria

1. WHEN un serveur secondaire ouvre un stream QUIC THEN il SHALL inclure des métadonnées identifiant le stream KWIK cible
2. WHEN des données transitent sur un stream secondaire THEN elles SHALL être encapsulées avec l'ID du stream KWIK et l'offset
3. WHEN le client reçoit des données sur un stream secondaire THEN il SHALL extraire les métadonnées pour router vers le bon stream KWIK
4. WHEN des streams secondaires sont créés THEN ils SHALL utiliser un protocole de communication interne standardisé
5. WHEN la communication interne échoue THEN le système SHALL gérer les erreurs sans affecter l'interface publique

### Requirement 5 - Mapping des Streams Logiques

**User Story:** En tant que système KWIK, je veux maintenir un mapping précis entre les streams QUIC des serveurs secondaires et les streams KWIK logiques pour que l'agrégation soit correcte.

#### Acceptance Criteria

1. WHEN un stream secondaire est créé THEN le système SHALL enregistrer son mapping vers le stream KWIK correspondant
2. WHEN des données arrivent sur un stream secondaire THEN le système SHALL utiliser le mapping pour identifier le stream KWIK cible
3. WHEN un stream KWIK est fermé THEN tous les streams secondaires associés SHALL être fermés automatiquement
4. WHEN un stream secondaire est fermé THEN le mapping SHALL être mis à jour sans affecter le stream KWIK si d'autres sources sont actives
5. WHEN le mapping est corrompu THEN le système SHALL détecter l'erreur et tenter une récupération

### Requirement 6 - Gestion des Offsets Multi-Sources

**User Story:** En tant que système KWIK, je veux gérer correctement les offsets quand plusieurs serveurs (principal et secondaires) écrivent dans le même stream logique pour que les données soient ordonnées correctement.

#### Acceptance Criteria

1. WHEN le serveur principal écrit dans un stream THEN les données SHALL être positionnées selon l'offset KWIK spécifié
2. WHEN un serveur secondaire écrit dans le même stream THEN ses données SHALL être positionnées selon leur offset KWIK sans conflit
3. WHEN des données arrivent avec des offsets qui se chevauchent THEN le système SHALL gérer les conflits selon une politique définie
4. WHEN des trous dans les offsets sont détectés THEN le système SHALL attendre les données manquantes ou signaler l'erreur
5. WHEN l'ordre des données est reconstitué THEN il SHALL respecter strictement les offsets KWIK indépendamment de la source

### Requirement 7 - Isolation des Plans de Données

**User Story:** En tant que système KWIK, je veux isoler les plans de données des serveurs secondaires tout en maintenant l'agrégation pour que la séparation des responsabilités soit claire.

#### Acceptance Criteria

1. WHEN des données transitent sur le plan de données d'un serveur secondaire THEN elles SHALL rester isolées de la session cliente publique
2. WHEN l'agrégation est effectuée THEN elle SHALL se faire au niveau du plan de données KWIK interne
3. WHEN des streams secondaires transportent des données THEN ils SHALL utiliser leur propre espace de numérotation QUIC
4. WHEN des données sont agrégées THEN elles SHALL être converties dans l'espace de numérotation KWIK unifié
5. WHEN l'isolation est maintenue THEN les performances ne SHALL pas être dégradées par rapport à l'architecture actuelle

### Requirement 8 - Gestion des Erreurs des Streams Secondaires

**User Story:** En tant que système KWIK, je veux gérer robustement les erreurs des streams secondaires pour que les pannes locales n'affectent pas l'ensemble du système.

#### Acceptance Criteria

1. WHEN un stream secondaire échoue THEN l'erreur SHALL être gérée en interne sans affecter le stream KWIK correspondant
2. WHEN un serveur secondaire devient indisponible THEN ses streams SHALL être fermés proprement avec notification interne
3. WHEN des données corrompues arrivent sur un stream secondaire THEN elles SHALL être rejetées sans affecter les autres sources
4. WHEN une erreur d'agrégation se produit THEN le système SHALL tenter une récupération ou isoler la source problématique
5. WHEN des erreurs critiques surviennent THEN elles SHALL être remontées au niveau approprié avec contexte suffisant

### Requirement 9 - Compatibilité avec l'Architecture Existante

**User Story:** En tant que développeur KWIK, je veux que cette modification soit compatible avec l'architecture existante pour que la migration soit transparente.

#### Acceptance Criteria

1. WHEN l'interface publique KWIK est utilisée THEN elle SHALL rester identique à l'implémentation actuelle
2. WHEN des applications existantes utilisent KWIK THEN elles SHALL continuer à fonctionner sans modification
3. WHEN le serveur principal ouvre des streams THEN le comportement SHALL être identique à l'implémentation actuelle
4. WHEN des métriques sont collectées THEN elles SHALL inclure les informations sur les streams secondaires internes
5. WHEN des tests existants sont exécutés THEN ils SHALL passer sans modification pour les fonctionnalités du serveur principal

### Requirement 10 - Performance et Optimisation

**User Story:** En tant que système KWIK, je veux maintenir des performances optimales malgré la complexité supplémentaire de l'agrégation des streams secondaires.

#### Acceptance Criteria

1. WHEN des streams secondaires sont agrégés THEN la latence supplémentaire SHALL être minimale (< 1ms)
2. WHEN de nombreux streams secondaires sont actifs THEN l'utilisation mémoire SHALL rester dans des limites acceptables
3. WHEN l'agrégation est effectuée THEN elle SHALL utiliser des algorithmes optimisés pour les performances
4. WHEN des données volumineuses transitent THEN le débit SHALL être maintenu proche du débit QUIC natif
5. WHEN le système est sous charge THEN les streams secondaires ne SHALL pas dégrader les performances des streams principaux