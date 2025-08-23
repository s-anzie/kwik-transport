# Requirements Document - Multi-path Transfer Reliability Fixes

## Introduction

Ce document définit les exigences pour corriger les problèmes critiques identifiés dans le système de transfert multi-path KWIK où seulement 9 chunks sur 15 sont reçus (arrêt à 63.2% du transfert), causant des transferts incomplets et des timeouts. L'analyse des logs révèle des problèmes de synchronisation entre serveurs, de détection de chunks manquants, et un mécanisme d'ACK applicatif défaillant.

## Requirements

### Requirement 1 - Garantie de réception complète des chunks

**User Story:** En tant que client KWIK, je veux recevoir la totalité des chunks de données (0 à N-1) de manière fiable et détectable, afin que le transfert de fichier soit complet et vérifiable.

#### Acceptance Criteria

1. WHEN le serveur primaire envoie les chunks pairs (0,2,4,6,8,10,12,14) THEN le client SHALL recevoir et traiter tous ces chunks
2. WHEN le serveur secondaire envoie les chunks impairs (1,3,5,7,9,11,13) THEN le client SHALL recevoir et traiter tous ces chunks  
3. WHEN un chunk est manquant dans la séquence THEN le client SHALL détecter le gap et logger l'erreur
4. WHEN tous les chunks attendus sont reçus THEN le client SHALL valider l'intégrité de la séquence complète
5. WHEN le transfert est terminé THEN le client SHALL avoir reçu exactement N chunks (où N = ceil(fileSize/chunkSize))

### Requirement 2 - Détection proactive des erreurs silencieuses

**User Story:** En tant que système KWIK, je veux détecter automatiquement les chunks manquants, dupliqués ou corrompus, afin d'éviter les erreurs silencieuses et les transferts incomplets.

#### Acceptance Criteria

1. WHEN un chunk arrive THEN le système SHALL vérifier que son ID est dans la plage attendue [0, N-1]
2. WHEN un chunk dupliqué arrive THEN le système SHALL détecter et logger la duplication
3. WHEN un gap dans la séquence est détecté THEN le système SHALL logger les chunks manquants avec leurs IDs
4. WHEN le transfert atteint un timeout THEN le système SHALL reporter quels chunks sont manquants
5. WHEN la taille totale reçue ne correspond pas à la taille attendue THEN le système SHALL signaler l'erreur

### Requirement 3 - Suppression de l'ACK applicatif défaillant

**User Story:** En tant que serveur primaire, je veux terminer proprement le transfert sans attendre d'ACK applicatif du client, afin d'éviter les timeouts et fermetures prématurées.

#### Acceptance Criteria

1. WHEN le serveur primaire a envoyé tous ses chunks pairs THEN il SHALL fermer sa session sans attendre d'ACK
2. WHEN le serveur secondaire a envoyé tous ses chunks impairs THEN il SHALL fermer sa session sans attendre d'ACK
3. WHEN un serveur termine l'envoi de ses chunks THEN il SHALL utiliser uniquement les mécanismes QUIC natifs pour la fermeture
4. IF un serveur ne peut pas envoyer un chunk THEN il SHALL logger l'erreur et continuer avec les chunks suivants
5. WHEN tous les chunks sont envoyés par un serveur THEN la session SHALL se fermer dans les 5 secondes maximum

### Requirement 4 - Synchronisation robuste des chemins multiples

**User Story:** En tant que système multi-path, je veux coordonner efficacement l'envoi des chunks entre le serveur primaire et secondaire, afin d'assurer une distribution équilibrée et complète.

#### Acceptance Criteria

1. WHEN le serveur primaire démarre l'envoi THEN il SHALL envoyer uniquement les chunks pairs (0,2,4,6,...)
2. WHEN le serveur secondaire reçoit une commande THEN il SHALL envoyer uniquement les chunks impairs (1,3,5,7,...)
3. WHEN un serveur ne peut pas envoyer un chunk THEN l'autre serveur SHALL continuer normalement
4. WHEN les deux serveurs envoient simultanément THEN il ne SHALL pas y avoir de collision d'IDs de chunks
5. WHEN un chunk est envoyé THEN il SHALL contenir son ID, offset, et taille pour validation côté client

### Requirement 5 - Logging et observabilité améliorés

**User Story:** En tant que développeur, je veux avoir une visibilité complète sur l'état du transfert multi-path, afin de diagnostiquer rapidement les problèmes de synchronisation et de perte de données.

#### Acceptance Criteria

1. WHEN un chunk est envoyé THEN le serveur SHALL logger [chunkID, offset, taille, chemin]
2. WHEN un chunk est reçu THEN le client SHALL logger [chunkID, offset, taille, chemin, timestamp]
3. WHEN le transfert se termine THEN le client SHALL logger un résumé [chunks reçus, chunks manquants, taille totale, durée]
4. WHEN une erreur de synchronisation survient THEN le système SHALL logger les détails de l'état des deux chemins
5. WHEN un timeout survient THEN le système SHALL logger l'état complet de la réception (bitmap des chunks reçus)

### Requirement 6 - Prévention des blocages de transport

**User Story:** En tant que protocole de transport KWIK, je veux garantir un flux de données continu sans blocages, afin d'éviter les deadlocks et les paralysies de transmission.

#### Acceptance Criteria

1. WHEN un chunk ne peut pas être envoyé immédiatement THEN le serveur SHALL continuer avec le chunk suivant sans bloquer
2. WHEN un chemin devient lent ou non-responsif THEN l'autre chemin SHALL continuer à transmettre normalement
3. WHEN le buffer de réception est plein THEN le client SHALL traiter les données existantes avant d'accepter de nouvelles données
4. WHEN une erreur de transmission survient sur un chemin THEN le système SHALL basculer temporairement sur l'autre chemin
5. WHEN un timeout de lecture survient THEN le système SHALL continuer le traitement des autres chunks disponibles

### Requirement 7 - Détection et diagnostic des deadlocks

**User Story:** En tant que système de diagnostic, je veux détecter proactivement tous les types de blocages dans le transport multi-path, afin de résoudre rapidement les problèmes de performance.

#### Acceptance Criteria

1. WHEN un thread de lecture est bloqué plus de 5 secondes THEN le système SHALL logger un warning de deadlock potentiel
2. WHEN un buffer reste plein plus de 10 secondes THEN le système SHALL diagnostiquer la cause du blocage
3. WHEN aucune donnée n'est reçue pendant 15 secondes THEN le système SHALL vérifier l'état des deux chemins
4. WHEN un chemin ne répond plus THEN le système SHALL logger l'état détaillé de ce chemin (RTT, paquets en attente, etc.)
5. WHEN un deadlock est détecté THEN le système SHALL logger la stack trace complète et l'état de tous les composants

### Requirement 8 - Mécanismes de récupération non-bloquants

**User Story:** En tant que système résilient, je veux implémenter des mécanismes de récupération qui ne bloquent pas le flux principal de données, afin de maintenir les performances même en cas d'erreurs.

#### Acceptance Criteria

1. WHEN un chunk est perdu THEN le système SHALL continuer à traiter les chunks suivants sans attendre
2. WHEN un chemin devient indisponible THEN le système SHALL redistribuer automatiquement la charge sur l'autre chemin
3. WHEN une reconnexion est nécessaire THEN elle SHALL se faire en arrière-plan sans bloquer les transferts en cours
4. WHEN un buffer overflow menace THEN le système SHALL implémenter une stratégie de backpressure non-bloquante
5. WHEN une erreur de parsing survient THEN le chunk défaillant SHALL être ignoré et le traitement SHALL continuer

### Requirement 9 - Validation d'intégrité du transfert

**User Story:** En tant que client, je veux valider automatiquement l'intégrité et la complétude du fichier reçu, afin de détecter immédiatement les transferts corrompus ou incomplets.

#### Acceptance Criteria

1. WHEN tous les chunks sont reçus THEN le client SHALL vérifier que la taille totale correspond à la taille attendue
2. WHEN le fichier est reconstitué THEN le client SHALL vérifier qu'aucun chunk n'est manquant dans la séquence
3. WHEN une validation échoue THEN le client SHALL logger précisément quelle validation a échoué
4. WHEN le transfert est validé THEN le client SHALL confirmer la réception complète dans les logs
5. IF la validation échoue THEN le client SHALL reporter les chunks manquants ou corrompus avec leurs IDs