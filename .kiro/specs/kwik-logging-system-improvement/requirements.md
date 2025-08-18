# Requirements Document

## Introduction

Le protocole KWIK souffre actuellement d'une verbosité excessive qui pollue la sortie des applications utilisatrices. Bien qu'un système de logging centralisé existe (`logger.go`), de nombreux composants utilisent encore des logs directs (`fmt.Printf`, `log.Printf`) qui ne respectent pas les niveaux de log configurés. De plus, il n'existe pas de mécanisme de configuration externe pour définir le niveau de verbosité souhaité.

Cette fonctionnalité vise à centraliser complètement le système de logging, permettre la configuration externe des niveaux de log, et assurer que les applications peuvent s'exprimer sans pollution de logs du protocole.

## Requirements

### Requirement 1

**User Story:** En tant que développeur utilisant le protocole KWIK, je veux pouvoir configurer le niveau de verbosité des logs via un fichier de configuration ou des variables d'environnement, afin que mon application puisse s'exprimer sans être polluée par les logs internes du protocole.

#### Acceptance Criteria

1. WHEN une configuration de log est fournie via un fichier YAML THEN le système KWIK SHALL utiliser ce niveau de log pour tous ses composants
2. WHEN une variable d'environnement KWIK_LOG_LEVEL est définie THEN le système KWIK SHALL utiliser cette valeur comme niveau de log par défaut
3. WHEN aucune configuration n'est fournie THEN le système KWIK SHALL utiliser un niveau de log par défaut (WARN ou ERROR) qui minimise la verbosité
4. WHEN le niveau de log est configuré à SILENT THEN le système KWIK SHALL ne produire aucun log sauf les erreurs critiques

### Requirement 2

**User Story:** En tant que développeur maintenant le protocole KWIK, je veux que tous les logs directs (`fmt.Printf`, `log.Printf`) soient remplacés par des appels au logger centralisé, afin d'avoir un contrôle uniforme sur la verbosité du système.

#### Acceptance Criteria

1. WHEN le code KWIK est exécuté THEN aucun log direct (`fmt.Printf`, `log.Printf`) ne SHALL être utilisé dans les composants internes
2. WHEN un composant a besoin de logger THEN il SHALL utiliser le logger centralisé fourni par le système KWIK
3. WHEN un nouveau composant est ajouté THEN il SHALL recevoir une instance du logger centralisé lors de son initialisation
4. WHEN des logs de debug sont nécessaires THEN ils SHALL être marqués avec le niveau DEBUG et ne s'afficher que si ce niveau est activé

### Requirement 3

**User Story:** En tant qu'utilisateur du protocole KWIK, je veux pouvoir activer des logs détaillés uniquement pour certains composants spécifiques, afin de déboguer des problèmes sans être submergé par tous les logs du système.

#### Acceptance Criteria

1. WHEN la configuration de log spécifie des niveaux par composant THEN chaque composant SHALL respecter son niveau de log individuel
2. WHEN un composant n'a pas de niveau spécifique configuré THEN il SHALL utiliser le niveau de log global
3. WHEN les logs par composant sont configurés THEN les composants suivants SHALL être supportés : session, transport, stream, data, control, presentation
4. IF un composant est configuré avec un niveau plus verbeux que le niveau global THEN ses logs SHALL s'afficher selon son niveau spécifique

### Requirement 4

**User Story:** En tant que développeur d'application utilisant KWIK, je veux que les logs d'erreur critiques soient toujours visibles même avec un niveau de log minimal, afin de pouvoir diagnostiquer les problèmes de connexion sans activer tous les logs de debug.

#### Acceptance Criteria

1. WHEN une erreur critique survient THEN elle SHALL toujours être loggée même si le niveau de log est configuré à SILENT
2. WHEN une erreur de connexion survient THEN elle SHALL être loggée avec suffisamment de contexte pour le diagnostic
3. WHEN une erreur de récupération automatique survient THEN elle SHALL être loggée au niveau WARN minimum
4. WHEN le système détecte une configuration de log invalide THEN il SHALL logger un avertissement et utiliser la configuration par défaut

### Requirement 5

**User Story:** En tant qu'administrateur système, je veux pouvoir configurer la destination des logs (stdout, stderr, fichier) et leur format, afin d'intégrer les logs KWIK dans mon système de monitoring existant.

#### Acceptance Criteria

1. WHEN la configuration spécifie une destination de log THEN tous les logs SHALL être dirigés vers cette destination
2. WHEN la configuration spécifie un format de log THEN tous les logs SHALL utiliser ce format (JSON, text, structured)
3. WHEN la rotation de logs est configurée THEN le système SHALL gérer automatiquement la rotation des fichiers de log
4. IF la destination de log configurée n'est pas accessible THEN le système SHALL fallback vers stdout avec un avertissement