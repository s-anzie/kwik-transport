# Requirements Document

## Introduction

KWIK (QUIC With Intelligent Konnections) est un protocole de transport basé sur QUIC-go qui permet à un client de communiquer simultanément avec plusieurs serveurs de manière transparente. Le protocole maintient une interface identique à QUIC tout en gérant en interne un agrégat complexe de connexions QUIC distinctes avec des plans de données et de contrôle sophistiqués.

## Requirements

### Requirement 1 - Interface Compatible QUIC

**User Story:** En tant que développeur, je veux utiliser une interface identique à QUIC (Dial, Listen, OpenStreamSync, AcceptStream) pour que je puisse migrer facilement depuis QUIC standard sans changer mon code applicatif.

#### Acceptance Criteria

1. WHEN le développeur appelle Dial() THEN le système SHALL retourner un objet Session avec les mêmes méthodes que QUIC
2. WHEN le développeur appelle OpenStreamSync() sur une Session THEN le système SHALL retourner un Stream avec l'interface QUIC standard
3. WHEN le développeur appelle AcceptStream() sur une Session THEN le système SHALL retourner un Stream compatible QUIC
4. WHEN le développeur utilise Listen() THEN le système SHALL créer un listener compatible avec l'interface QUIC

### Requirement 2 - Établissement du Chemin Principal

**User Story:** En tant que client KWIK, je veux établir automatiquement un chemin principal lors du Dial() pour que j'aie une connexion de base fonctionnelle vers le serveur principal.

#### Acceptance Criteria

1. WHEN un client fait Dial() avec l'adresse du serveur principal THEN le système SHALL créer une connexion QUIC vers ce serveur
2. WHEN la connexion QUIC est établie THEN le système SHALL ouvrir automatiquement un flux de plan de contrôle
3. WHEN le flux de contrôle est ouvert THEN le système SHALL s'authentifier et recevoir un identifiant unique de session
4. WHEN l'authentification réussit THEN le chemin SHALL être marqué comme chemin principal (par défaut)
5. WHEN le chemin principal est établi THEN il SHALL servir de chemin par défaut pour les opérations de flux

### Requirement 3 - Établissement des Chemins Secondaires

**User Story:** En tant que serveur KWIK, je veux pouvoir demander l'établissement de chemins secondaires vers d'autres serveurs pour que le client puisse bénéficier de connexions multiples transparentes.

#### Acceptance Criteria

1. WHEN le serveur appelle addPath() avec l'adresse d'un serveur secondaire THEN le système SHALL envoyer une trame de commande d'ajout via le plan de contrôle
2. WHEN le client reçoit la trame d'ajout de chemin THEN le système SHALL interpréter la commande et initier un Dial() QUIC vers le serveur secondaire spécifié
3. WHEN la connexion QUIC vers le serveur secondaire est établie THEN le système SHALL ouvrir un flux de plan de contrôle sur ce nouveau chemin
4. WHEN le flux de contrôle du chemin secondaire est ouvert THEN le système SHALL s'authentifier avec l'identifiant de session existant
5. WHEN l'établissement du chemin secondaire réussit THEN le client SHALL notifier le succès au serveur via une trame de réponse sur le plan de contrôle
6. WHEN l'établissement du chemin secondaire échoue THEN le client SHALL notifier l'échec au serveur via une trame de réponse sur le plan de contrôle
7. WHEN un chemin secondaire est établi avec succès THEN il SHALL être ajouté à l'agrégat de la session cliente et disponible pour la distribution du trafic

### Requirement 4 - Gestion des Plans de Données et de Contrôle

**User Story:** En tant que système KWIK, je veux séparer les plans de données et de contrôle pour que je puisse gérer efficacement les flux de données et les commandes de gestion des chemins.

#### Acceptance Criteria

1. WHEN des données applicatives transitent THEN le système SHALL utiliser exclusivement les flux du plan de données
2. WHEN des commandes de gestion de chemins sont émises THEN le système SHALL utiliser exclusivement les flux du plan de contrôle
3. WHEN un Stream.Write() est appelé côté client THEN les données SHALL toujours transiter vers le serveur principal via le plan de données
4. WHEN un Stream.Write() est appelé côté serveur THEN les données SHALL transiter par le plan de données de ce serveur
5. WHEN un Stream.Read() est appelé côté serveur THEN les données SHALL provenir du plan de données de ce serveur
6. WHEN un Stream.Read() est appelé côté client THEN les données SHALL être agrégées depuis les plans de données de tous les chemins actifs

### Requirement 5 - Session Multi-Chemins Côté Client

**User Story:** En tant que session cliente KWIK, je veux agréger automatiquement les connexions vers plusieurs serveurs pour que le développeur bénéficie de performances améliorées sans en être conscient.

#### Acceptance Criteria

1. WHEN la session cliente reçoit des données de plusieurs chemins THEN le système SHALL les agréger de manière cohérente
2. WHEN la session cliente envoie des données THEN le système SHALL les envoyer uniquement au serveur principal via le chemin principal
3. WHEN un nouveau chemin secondaire est ajouté THEN le système SHALL l'intégrer automatiquement dans l'agrégat
4. WHEN un chemin devient indisponible THEN le client SHALL le marquer comme mort et notifier le serveur via une trame spécifique sur le plan de contrôle
5. WHEN des chemins sont ajoutés ou retirés THEN l'interface publique de la session SHALL rester inchangée

### Requirement 6 - Gestion des Chemins Côté Serveur

**User Story:** En tant que serveur KWIK, je veux pouvoir contrôler dynamiquement les chemins pour que je puisse optimiser la topologie de connexion selon mes besoins.

#### Acceptance Criteria

1. WHEN le serveur demande l'ajout d'un chemin secondaire THEN le système SHALL envoyer la commande avec l'adresse du serveur cible via le plan de contrôle
2. WHEN le serveur demande le retrait d'un chemin THEN le système SHALL envoyer la commande de fermeture via le plan de contrôle
3. WHEN le serveur commande une opération vers un chemin mort THEN le système SHALL envoyer une trame de chemin mort et l'opération ne SHALL pas passer
4. WHEN le serveur demande les chemins actifs THEN le système SHALL retourner la liste des chemins opérationnels
5. WHEN le serveur demande les chemins morts THEN le système SHALL retourner la liste des chemins non-opérationnels
6. WHEN le serveur demande tous les chemins THEN le système SHALL retourner l'historique complet depuis le début de session

### Requirement 7 - Optimisation des Flux Logiques

**User Story:** En tant que système KWIK, je veux optimiser l'utilisation des flux QUIC réels pour que je puisse supporter plusieurs flux logiques KWIK avec des performances optimales.

#### Acceptance Criteria

1. WHEN un OpenStream() est appelé sur une session THEN le système SHALL identifier le chemin par défaut et créer un flux logique KWIK
2. WHEN plusieurs flux logiques sont créés THEN le système SHALL utiliser des commandes de contrôle pour notifier leur création avec identifiants
3. WHEN des flux logiques sont utilisés THEN le système SHALL utiliser un seul flux QUIC réel pour desservir 3-4 flux logiques
4. WHEN le ratio flux logiques/flux réels dépasse le seuil THEN le système SHALL créer automatiquement de nouveaux flux QUIC réels
5. WHEN le trafic diminue THEN le système SHALL réduire automatiquement le nombre de flux QUIC réels

### Requirement 8 - Gestion des Trames et Paquets

**User Story:** En tant que système KWIK, je veux utiliser des représentations de trames et paquets pour que je puisse identifier et transporter efficacement les données des flux logiques.

#### Acceptance Criteria

1. WHEN des données de flux logiques sont transmises THEN le système SHALL les encapsuler dans des trames avec identifiant de flux logique
2. WHEN des trames sont reçues THEN le système SHALL les router vers le flux logique correspondant selon l'identifiant
3. WHEN des paquets sont traités THEN le système SHALL maintenir les espaces de numérotation par chemin
4. WHEN des données sont réassemblées THEN le système SHALL respecter les offsets dans le flux et l'ordre correct
5. WHEN le scheduling d'envoi est effectué THEN le système SHALL optimiser selon les performances des chemins

### Requirement 9 - Transmission de Paquets Non-KWIK

**User Story:** En tant que serveur KWIK custom, je veux pouvoir transmettre des données brutes non-KWIK via des chemins spécifiques pour que je puisse implémenter des protocoles personnalisés.

#### Acceptance Criteria

1. WHEN le serveur demande l'envoi de données brutes THEN le système SHALL accepter les données avec spécification du chemin cible
2. WHEN des données brutes sont reçues via le plan de contrôle THEN le client interne SHALL identifier le chemin cible
3. WHEN le chemin cible est identifié THEN le système SHALL faire transiter les données sur le plan de données de ce chemin
4. WHEN le serveur cible lit sur son plan de données THEN il SHALL recevoir les données brutes envoyées depuis le plan de contrôle d'un autre serveur
5. WHEN des données brutes transitent THEN le système SHALL maintenir leur intégrité sans interprétation

### Requirement 10 - Gestion Robuste des Flux et Reconstitution

**User Story:** En tant que système KWIK, je veux gérer robustement les flux et leur reconstitution pour que je garantisse l'intégrité et l'ordre des données malgré la complexité multi-chemins.

#### Acceptance Criteria

1. WHEN des flux sont multiplexés sur plusieurs chemins THEN le système SHALL maintenir l'isolation entre flux logiques
2. WHEN des données arrivent en désordre depuis différents chemins THEN le système SHALL les réordonner selon les offsets
3. WHEN un chemin devient indisponible THEN le système SHALL marquer le chemin comme mort et cesser de l'utiliser pour les nouveaux flux
4. WHEN des flux sont reconstitués THEN le système SHALL garantir l'intégrité des données agrégées
5. WHEN le management des flux est effectué THEN le système SHALL maintenir une gestion efficace de la reconstitution et du séquençage

### Requirement 11 - Gestion Optimale des Accusés de Réception

**User Story:** En tant que système KWIK, je veux gérer les accusés de réception (ACK) de manière optimale pour que je garantisse des performances maximales et une fiabilité élevée sur tous les chemins.

#### Acceptance Criteria

1. WHEN des paquets sont reçus sur un chemin THEN le système SHALL générer des ACK optimisés selon les caractéristiques du chemin
2. WHEN des ACK sont envoyés THEN le système SHALL les optimiser pour minimiser la latence et maximiser le débit
3. WHEN des paquets sont perdus THEN le système SHALL détecter rapidement la perte et déclencher les retransmissions appropriées
4. WHEN des ACK sont reçus THEN le système SHALL mettre à jour efficacement les fenêtres de congestion par chemin
5. WHEN plusieurs chemins sont actifs THEN le système SHALL coordonner les ACK pour éviter les interférences entre chemins

### Requirement 12 - Structure du Projet et Sérialisation

**User Story:** En tant que développeur KWIK, je veux une structure de projet bien organisée utilisant protobuf pour que le code soit maintenable et les données sérialisées efficacement.

#### Acceptance Criteria

1. WHEN le projet est organisé THEN il SHALL avoir une structure claire séparant les composants (session, stream, transport, protocol)
2. WHEN des données sont sérialisées THEN le système SHALL utiliser protobuf pour toutes les trames de contrôle et de données
3. WHEN des messages protobuf sont définis THEN ils SHALL couvrir tous les types de trames (contrôle, données, ACK, notifications)
4. WHEN le code est structuré THEN il SHALL séparer clairement les interfaces publiques des implémentations internes
5. WHEN des modules sont créés THEN ils SHALL avoir des responsabilités bien définies et des interfaces claires

### Requirement 13 - Gestion des Tailles de Paquets et Offsets

**User Story:** En tant que système KWIK, je veux calculer précisément les tailles de paquets par rapport aux capacités QUIC pour que je ne confonde pas les offsets KWIK avec les offsets QUIC.

#### Acceptance Criteria

1. WHEN des paquets KWIK sont créés THEN le système SHALL calculer leur taille par rapport à la taille maximale que QUIC peut transporter
2. WHEN des données sont fragmentées THEN le système SHALL maintenir des offsets KWIK distincts des offsets QUIC sous-jacents
3. WHEN des paquets sont assemblés THEN le système SHALL s'assurer qu'ils respectent les limites de taille QUIC
4. WHEN des offsets sont gérés THEN le système SHALL maintenir une correspondance claire entre offsets KWIK logiques et offsets QUIC physiques
5. WHEN des données sont reconstituées THEN le système SHALL utiliser les offsets KWIK pour l'ordre logique et les offsets QUIC pour le transport physique