# Requirements Document

## Introduction

Ce document définit les exigences pour un système de transfert de fichier multi-path utilisant le protocole KWIK. Le système permettra au client de télécharger un fichier depuis le serveur principal, tandis que le serveur principal coordonnera avec le serveur secondaire pour envoyer des portions du fichier en parallèle, optimisant ainsi la bande passante et la vitesse de transfert.

## Requirements

### Requirement 1 - Architecture de transfert distribué

**User Story:** En tant que client, je veux télécharger un fichier en utilisant plusieurs chemins simultanément, afin d'optimiser la vitesse de transfert et la résilience.

#### Acceptance Criteria

1. WHEN le client demande un fichier THEN le système SHALL diviser le fichier en chunks
2. WHEN le transfert commence THEN le serveur principal SHALL envoyer certains chunks directement au client
3. WHEN le transfert commence THEN le serveur principal SHALL coordonner avec le serveur secondaire pour envoyer d'autres chunks
4. WHEN tous les chunks sont reçus THEN le client SHALL reconstituer le fichier complet

### Requirement 2 - Gestion des chunks de fichier

**User Story:** En tant que système, je veux diviser les fichiers en chunks numérotés, afin de permettre un transfert parallèle et une reconstruction ordonnée.

#### Acceptance Criteria

1. WHEN un fichier est demandé THEN le système SHALL le diviser en chunks de taille fixe (ex: 64KB)
2. WHEN un chunk est créé THEN il SHALL avoir un numéro de séquence unique
3. WHEN un chunk est envoyé THEN il SHALL inclure des métadonnées (numéro, taille, checksum)
4. WHEN le dernier chunk est créé THEN il SHALL être marqué comme chunk final

### Requirement 3 - Coordination serveur principal/secondaire

**User Story:** En tant que serveur principal, je veux coordonner avec le serveur secondaire pour distribuer les chunks, afin d'optimiser l'utilisation de la bande passante.

#### Acceptance Criteria

1. WHEN le transfert commence THEN le serveur principal SHALL déterminer quels chunks envoyer lui-même
2. WHEN le transfert commence THEN le serveur principal SHALL envoyer des ordres au serveur secondaire via SendRawData
3. WHEN le serveur secondaire reçoit un ordre THEN il SHALL envoyer le chunk demandé au client
4. WHEN un chunk est envoyé THEN le serveur SHALL notifier la completion au serveur principal

### Requirement 4 - Protocole de commande pour chunks

**User Story:** En tant que serveur principal, je veux envoyer des commandes structurées au serveur secondaire, afin de coordonner l'envoi de chunks spécifiques.

#### Acceptance Criteria

1. WHEN le serveur principal veut déléguer un chunk THEN il SHALL envoyer une commande "SEND_CHUNK"
2. WHEN la commande est envoyée THEN elle SHALL inclure le numéro de chunk et les métadonnées
3. WHEN le serveur secondaire reçoit une commande THEN il SHALL valider les paramètres
4. WHEN la commande est valide THEN le serveur secondaire SHALL envoyer le chunk au client

### Requirement 5 - Gestion des erreurs et retry

**User Story:** En tant que système, je veux gérer les échecs de transfert de chunks, afin d'assurer la fiabilité du transfert complet.

#### Acceptance Criteria

1. WHEN un chunk n'est pas reçu dans le délai THEN le client SHALL demander un retry
2. WHEN un chunk est corrompu THEN le client SHALL demander une retransmission
3. WHEN un serveur ne répond pas THEN le système SHALL basculer vers l'autre serveur
4. WHEN tous les retries échouent THEN le système SHALL signaler l'erreur au client

### Requirement 6 - Interface client pour transfert de fichier

**User Story:** En tant que client, je veux une interface simple pour demander un fichier, afin de déclencher le transfert multi-path.

#### Acceptance Criteria

1. WHEN le client veut un fichier THEN il SHALL appeler DownloadFile(filename)
2. WHEN le téléchargement commence THEN le client SHALL recevoir des callbacks de progression
3. WHEN un chunk arrive THEN le client SHALL le stocker temporairement
4. WHEN tous les chunks sont reçus THEN le client SHALL reconstituer et sauvegarder le fichier

### Requirement 7 - Optimisation de la distribution des chunks

**User Story:** En tant que système, je veux distribuer intelligemment les chunks entre les serveurs, afin d'optimiser les performances de transfert.

#### Acceptance Criteria

1. WHEN le transfert commence THEN le système SHALL analyser la bande passante de chaque chemin
2. WHEN la distribution est calculée THEN les chunks SHALL être assignés selon la capacité de chaque serveur
3. WHEN un serveur est plus rapide THEN il SHALL recevoir plus de chunks à envoyer
4. WHEN un serveur ralentit THEN la distribution SHALL être réajustée dynamiquement

### Requirement 8 - Intégrité et validation des données

**User Story:** En tant que système, je veux valider l'intégrité de chaque chunk et du fichier final, afin d'assurer la qualité des données transférées.

#### Acceptance Criteria

1. WHEN un chunk est créé THEN il SHALL inclure un checksum MD5 ou SHA256
2. WHEN un chunk est reçu THEN le client SHALL valider son checksum
3. WHEN tous les chunks sont reçus THEN le client SHALL valider le checksum du fichier complet
4. WHEN une validation échoue THEN le système SHALL demander une retransmission