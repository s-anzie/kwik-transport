# Implementation Plan

- [x] 1. Créer les structures de base pour l'isolation des streams secondaires
  - Implémenter les interfaces et structures de données pour la gestion des streams secondaires
  - Créer les types de base pour le mapping et les métadonnées
  - _Requirements: 1.1, 1.2, 1.3_

- [x] 1.1 Implémenter les interfaces pour le Secondary Stream Handler
  - Créer le fichier `kwik/pkg/stream/secondary_handler.go` avec l'interface `SecondaryStreamHandler`
  - Définir les structures `SecondaryStreamInfo`, `SecondaryStreamStats`, et `SecondaryStreamConfig`
  - Implémenter les méthodes de base pour la gestion des streams secondaires
  - _Requirements: 2.1, 2.2_

- [x] 1.2 Créer le protocole de métadonnées pour les streams internes
  - Créer le fichier `kwik/pkg/stream/metadata_protocol.go` avec l'interface `MetadataProtocol`
  - Définir les structures `StreamMetadata` et les types `MetadataType`
  - Implémenter les méthodes d'encapsulation et décapsulation des métadonnées
  - _Requirements: 4.1, 4.2, 4.3_

- [x] 1.3 Implémenter le gestionnaire de mappings des streams
  - Créer le fichier `kwik/pkg/stream/mapping_manager.go` avec la structure `StreamMappingManager`
  - Définir les structures `MappingMetadata` et les méthodes de gestion des mappings
  - Implémenter la logique de création, mise à jour et suppression des mappings
  - _Requirements: 5.1, 5.2, 5.3_

- [x] 2. Développer le système d'agrégation des streams secondaires
  - Créer les composants pour agréger les données des streams secondaires dans les streams KWIK
  - Implémenter la gestion des offsets multi-sources
  - _Requirements: 3.1, 3.2, 3.3_

- [x] 2.1 Créer l'agrégateur spécialisé pour les streams secondaires
  - Créer le fichier `kwik/pkg/data/secondary_aggregator.go` avec la structure `SecondaryStreamAggregator`
  - Définir les structures `SecondaryStreamData` et les méthodes d'agrégation
  - Implémenter la logique d'agrégation des données avec gestion des offsets
  - _Requirements: 3.1, 3.2, 3.3_

- [x] 2.2 Implémenter le gestionnaire d'offsets multi-sources
  - Créer le fichier `kwik/pkg/data/multi_source_offset_manager.go` avec la structure `MultiSourceOffsetManager`
  - Définir les structures `KwikStreamOffsetState` et `OffsetManagerConfig`
  - Implémenter la logique de synchronisation et résolution de conflits d'offsets
  - _Requirements: 6.1, 6.2, 6.3_

- [x] 2.3 Étendre l'interface StreamAggregator existante
  - Modifier le fichier `kwik/pkg/data/aggregator.go` pour ajouter les méthodes pour streams secondaires
  - Ajouter les méthodes `AggregateSecondaryData`, `SetStreamMapping`, `RemoveStreamMapping`
  - Implémenter la validation des offsets et la gestion des données multi-sources
  - _Requirements: 3.4, 3.5_

- [x] 3. Créer les messages protobuf pour la communication interne
  - Définir les messages protobuf pour les métadonnées et la communication entre composants
  - Générer le code Go correspondant
  - _Requirements: 4.4, 4.5_

- [x] 3.1 Définir les messages protobuf pour les streams secondaires
  - Créer le fichier `kwik/proto/secondary_stream.proto` avec les messages `SecondaryStreamOpen`, `SecondaryStreamData`, `SecondaryStreamClose`
  - Ajouter les messages `OffsetSyncRequest` et `OffsetSyncResponse`
  - Générer le code Go avec `protoc`
  - _Requirements: 4.1, 4.2_

- [x] 3.2 Créer les messages protobuf pour les métadonnées
  - Créer le fichier `kwik/proto/stream_metadata.proto` avec les messages `StreamMetadataFrame` et `StreamMappingUpdate`
  - Définir les énumérations `MetadataFrameType` et `MappingOperation`
  - Générer le code Go correspondant
  - _Requirements: 4.3, 4.4_

- [x] 4. Modifier les sessions client et serveur pour intégrer l'isolation
  - Adapter les sessions existantes pour utiliser le nouveau système d'isolation
  - Implémenter la restriction des ouvertures de streams pour les serveurs secondaires
  - _Requirements: 1.1, 1.2, 1.3, 1.4_

- [x] 4.1 Modifier la session cliente pour intégrer le handler secondaire
  - Modifier `kwik/pkg/session/client.go` pour ajouter le `SecondaryStreamHandler`
  - Intégrer la gestion des streams secondaires dans `AcceptStream()` et `handleIncomingStreams()`
  - Ajouter les méthodes internes `getSecondaryStreamHandler()` et `handleSecondaryStreamOpen()`
  - _Requirements: 2.1, 2.2, 2.3_

- [x] 4.2 Modifier la session serveur pour restreindre les ouvertures
  - Modifier `kwik/pkg/session/server.go` pour ajouter la notion de `ServerRole`
  - Implémenter la restriction `OpenStreamSync()` aux serveurs principaux uniquement
  - Ajouter les méthodes `openSecondaryStream()` et `getServerRole()` pour serveurs secondaires
  - _Requirements: 1.1, 1.2, 1.3_

- [x] 4.3 Créer l'interface SecondaryStream pour les serveurs secondaires
  - Créer le fichier `kwik/pkg/stream/secondary_stream.go` avec l'interface `SecondaryStream`
  - Implémenter les méthodes `Write()`, `Close()`, `GetKwikStreamID()`, `SetKwikStreamMapping()`
  - Intégrer avec le protocole de métadonnées pour l'encapsulation des données
  - _Requirements: 2.4, 2.5_

- [x] 5. Implémenter la logique d'isolation et de routage interne
  - Créer les composants pour isoler les streams secondaires de la session publique
  - Implémenter le routage des données vers les streams KWIK appropriés
  - _Requirements: 2.1, 2.2, 2.3, 2.4_

- [x] 5.1 Créer le module d'isolation des streams
  - Créer le fichier `kwik/internal/stream/secondary_isolation.go` avec la logique d'isolation
  - Implémenter les fonctions de validation et de routage des streams secondaires
  - Ajouter la logique de prévention d'exposition des streams secondaires à l'interface publique
  - _Requirements: 2.1, 2.2, 2.3_

- [x] 5.2 Modifier le stream multiplexer pour supporter l'isolation
  - Modifier `kwik/pkg/stream/multiplexer.go` pour intégrer la gestion des streams secondaires
  - Ajouter la distinction entre streams publics et streams internes
  - Implémenter la logique de routage vers l'agrégateur approprié
  - _Requirements: 2.4, 2.5_

- [x] 5.3 Étendre le logical stream manager pour l'isolation
  - Modifier `kwik/pkg/stream/logical_manager.go` pour supporter les streams secondaires
  - Ajouter la gestion des mappings vers les streams KWIK
  - Implémenter la logique de création de streams logiques depuis les streams secondaires
  - _Requirements: 5.4, 5.5_

- [x] 6. Implémenter la gestion des erreurs spécialisée
  - Créer les handlers d'erreurs pour les cas spécifiques à l'isolation des streams
  - Implémenter les stratégies de récupération et de fallback
  - _Requirements: 8.1, 8.2, 8.3, 8.4_

- [x] 6.1 Créer les handlers d'erreurs pour les mappings
  - Créer le fichier `kwik/pkg/stream/mapping_error_handler.go` avec `MappingErrorHandler`
  - Implémenter la gestion des conflits de mapping et des timeouts
  - Ajouter les stratégies de retry et de récupération pour les mappings
  - _Requirements: 8.1, 8.2_

- [x] 6.2 Implémenter les handlers d'erreurs d'agrégation
  - Créer le fichier `kwik/pkg/data/aggregation_error_handler.go` avec `AggregationErrorHandler`
  - Implémenter la gestion des conflits d'offsets et de la corruption de données
  - Ajouter les stratégies de fallback pour l'agrégation
  - _Requirements: 8.3, 8.4_

- [x] 6.3 Créer les handlers d'erreurs de métadonnées
  - Créer le fichier `kwik/pkg/stream/metadata_error_handler.go` avec `MetadataErrorHandler`
  - Implémenter la gestion des violations de protocole et des timeouts
  - Ajouter les stratégies de récupération pour les métadonnées corrompues
  - _Requirements: 8.5_

- [x] 7. Créer les tests unitaires pour les nouveaux composants
  - Développer une suite de tests complète pour valider le fonctionnement de l'isolation
  - Tester les cas d'erreur et les scénarios de récupération
  - _Requirements: 9.1, 9.2, 9.3, 9.4_

- [x] 7.1 Créer les tests pour le Secondary Stream Handler
  - Créer le fichier `kwik/pkg/stream/secondary_handler_test.go`
  - Tester la gestion des streams secondaires, les mappings et les statistiques
  - Valider l'isolation des streams et la non-exposition à l'interface publique
  - _Requirements: 2.1, 2.2, 2.3_

- [x] 7.2 Créer les tests pour l'agrégation des streams secondaires
  - Créer le fichier `kwik/pkg/data/secondary_aggregator_test.go`
  - Tester l'agrégation multi-sources, la gestion des offsets et la résolution de conflits
  - Valider la performance et la correctness de l'agrégation
  - _Requirements: 3.1, 3.2, 3.3_

- [x] 7.3 Créer les tests pour le protocole de métadonnées
  - Créer le fichier `kwik/pkg/stream/metadata_protocol_test.go`
  - Tester l'encapsulation/décapsulation et la validation des métadonnées
  - Valider la gestion des erreurs de protocole
  - _Requirements: 4.1, 4.2, 4.3_

- [x] 7.4 Créer les tests pour la gestion des mappings
  - Créer le fichier `kwik/pkg/stream/mapping_manager_test.go`
  - Tester la création, mise à jour et suppression des mappings
  - Valider la résolution de conflits et le nettoyage automatique
  - _Requirements: 5.1, 5.2, 5.3_

- [ ] 8. Développer les tests d'intégration pour l'isolation complète
  - Créer des tests end-to-end pour valider le comportement complet du système
  - Tester les scénarios multi-serveurs avec agrégation
  - _Requirements: 9.5, 10.1, 10.2, 10.3_

- [x] 8.1 Créer les tests d'intégration multi-serveurs
  - Créer le fichier `kwik/pkg/session/secondary_isolation_integration_test.go`
  - Tester un scénario avec serveur principal + 2 serveurs secondaires
  - Valider l'agrégation transparente et l'interface publique inchangée
  - _Requirements: 9.1, 9.2, 9.3_

- [x] 8.2 Créer les tests de performance pour l'isolation
  - Créer le fichier `kwik/pkg/stream/secondary_isolation_bench_test.go`
  - Benchmarker l'overhead d'agrégation et de métadonnées
  - Valider que la latence supplémentaire reste < 1ms
  - _Requirements: 10.1, 10.2_

- [ ] 8.3 Créer les tests de compatibilité
  - Créer le fichier `kwik/pkg/session/compatibility_test.go`
  - Tester que les applications existantes fonctionnent sans modification
  - Valider que l'interface publique reste identique
  - _Requirements: 9.1, 9.2_

- [x] 9. Optimiser les performances et finaliser l'implémentation (evite le cache je n'en n'ai pas besoin retire meme tout en rapport avec le cache)
  - Implémenter les optimisations de performance identifiées
  - Finaliser la documentation et les métriques
  - _Requirements: 10.1, 10.2, 10.3, 10.4, 10.5_

- [x] 9.1 Implémenter les optimisations de cache et de pooling
  - Ajouter le cache des mappings dans `StreamMappingManager`
  - Implémenter le pooling des buffers d'agrégation
  - Optimiser la gestion mémoire avec des pools de streams secondaires
  - _Requirements: 10.1, 10.3_

- [x] 9.2 Implémenter le traitement par lots et parallèle
  - Ajouter le batch processing des métadonnées dans `MetadataProtocol`
  - Implémenter le traitement parallèle dans `SecondaryStreamAggregator`
  - Optimiser les opérations d'agrégation pour réduire la latence
  - _Requirements: 10.1, 10.2_

- [x] 9.3 Ajouter les métriques de performance et monitoring
  - Créer le fichier `kwik/pkg/stream/secondary_metrics.go` avec les métriques spécialisées
  - Implémenter le monitoring de la latence d'agrégation et de l'overhead
  - Ajouter les métriques de débit et d'efficacité d'agrégation
  - _Requirements: 10.4, 10.5_

- [x] 10. Intégrer avec l'architecture existante et valider la compatibilité
  - Finaliser l'intégration avec les composants KWIK existants
  - Valider la compatibilité ascendante et les performances
  - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5_

- [x] 10.1 Intégrer avec le transport layer existant
  - Modifier `kwik/pkg/transport/path.go` pour supporter les streams secondaires
  - Intégrer le `SecondaryStreamHandler` avec le `PathManager`
  - Valider que les chemins secondaires gèrent correctement les streams internes
  - _Requirements: 7.1, 7.2_

- [x] 10.2 Intégrer avec le control plane existant
  - Modifier `kwik/pkg/control/plane.go` pour supporter les métadonnées de streams secondaires
  - Ajouter la gestion des messages de synchronisation d'offsets
  - Intégrer les notifications de mapping avec le plan de contrôle
  - _Requirements: 7.3, 7.4_

- [x] 10.3 Valider l'intégration complète avec des tests end-to-end
  - Créer le fichier `kwik/integration_test/secondary_stream_isolation_test.go`
  - Tester un scénario complet de transfert de fichier avec agrégation
  - Valider les performances, la compatibilité et la correctness
  - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5_