# API de provisioning

L'API utilise un listener HTTPS dédié, distinct du proxy public `/v1/`.
Elle expose le manifeste et les cinq PUT du contrat App Covenant
`0.1.0-draft.1`, ainsi que les lectures communes, ETags, préconditions, listes
et flux de synchronisation des lots 1 à 3. Les webhooks restent prévus au
lot 4 ; cette version ne revendique pas une conformité complète au contrat.

## Configuration et certificats

| Variable | Défaut | Description |
| --- | --- | --- |
| `XOLO_PROVISIONNING_API_ENABLED` | `false` | Active le listener dédié |
| `XOLO_PROVISIONNING_API_ADDRESS` | `:3003` | Adresse d'écoute |
| `XOLO_PROVISIONNING_API_TLS_CERT_FILE` | — | Certificat serveur PEM |
| `XOLO_PROVISIONNING_API_TLS_KEY_FILE` | — | Clé privée serveur PEM |
| `XOLO_PROVISIONNING_API_TLS_CLIENT_CA_FILE` | — | Autorité de certification des clients |
| `XOLO_PROVISIONNING_API_AUTHORIZED_URIS` | — | Liste obligatoire d'URI absolues, séparées par des virgules |
| `XOLO_PROVISIONNING_API_RATE_LIMIT` | `10` | Requêtes par seconde et par URI autorisée |
| `XOLO_PROVISIONNING_API_RATE_BURST` | `20` | Rafale maximale par URI |
| `XOLO_PROVISIONNING_API_SHUTDOWN_TIMEOUT` | `10s` | Délai d'arrêt gracieux |

TLS 1.3 est obligatoire. Le certificat client doit être valide, signé par une
CA configurée et contenir exactement une URI SAN correspondant exactement à la
liste d'autorisation. Le CN, les DNS SAN, cookies, tokens utilisateur et
certificats transmis par en-têtes ne donnent aucun droit. Une URI autorisée
administre toute l'instance, y compris les ressources suspendues.

Exemple d'extension de certificat :

```ini
extendedKeyUsage=clientAuth
subjectAltName=URI:urn:example:console
```

Configurer alors `XOLO_PROVISIONNING_API_AUTHORIZED_URIS=urn:example:console`.
Les erreurs de certificat interrompent la négociation TLS ; le contrôle HTTP
de défense renvoie `403 client_certificate_rejected`.

Les budgets sont locaux à chaque réplique et remis à zéro au redémarrage.
Une limite atteinte renvoie `429 rate_limited`, sans mutation, avec un
`Retry-After` entier positif en secondes. Les clients doivent espacer leurs
réessais avec une part aléatoire.

`X-Request-ID` accepte une seule valeur de 32 caractères hexadécimaux minuscules.
Sinon, le serveur en génère une sans journaliser la valeur rejetée. La valeur
retenue accompagne toutes les réponses HTTP, les logs et les audits. Elle ne
constitue pas une clé d'idempotence.

## Routes communes

`GET /v1/manifest` renvoie uniquement `name`, `version` (version de Xolo) et
`contract_version`. Le client doit le relire avant les écritures et après une
mise à jour ou une reconnexion.

| PUT | Champs |
| --- | --- |
| `/v1/tenants/{tenantID}` | `slug`, `name`, `status` |
| `/v1/tenants/{tenantID}/domains/{hostname}` | `status` |
| `/v1/tenants/{tenantID}/organizations/{organizationID}` | `slug`, `name`, `status` |
| `/v1/tenants/{tenantID}/members/{memberID}` | `email`, `display_name` facultatif, `tenant_role`, `status` |
| `/v1/tenants/{tenantID}/organizations/{organizationID}/members/{memberID}` | `role`, `status` |

Les UUID sont fournis par le client, sous forme canonique en minuscules. Le rôle
de tenant est `owner` ou `member` ; celui d'adhésion est `owner`, `admin` ou
`member`. Le statut est `active` ou `suspended`. Tous les champs sauf
`display_name` sont obligatoires. Omettre `display_name` le vide.

Les PUT attendent un seul objet JSON, avec `Content-Type: application/json`
(paramètres valides acceptés). Les champs inconnus, types incorrects, JSON
supplémentaire, UTF-8 invalide et corps dépassant 1 048 576 octets, espaces finaux
compris, sont refusés par `400 invalid_json`. Les champs absents, nulls ou valeurs
invalides relèvent de `400 invalid_representation`. Un média type incorrect
renvoie `415 unsupported_media_type`.

Les slugs, e-mails et hostnames sont nettoyés aux extrémités et passés en
minuscules. Les noms sont nettoyés. Les rôles et statuts restent sensibles à la
casse. Le statut d'un membre n'est pas nettoyé : `" active "` est invalide.
Les limites après normalisation sont de 63 octets pour le slug, 200 pour les
noms, 320 pour l'e-mail et 253 pour le hostname. L'e-mail exige `@` et aucun
contrôle ASCII ; les noms refusent également les contrôles ASCII. Les domaines
sont des noms DNS ASCII, sans IP, port, chemin ou point final.

Chaque succès renvoie `200` avec la représentation normalisée seule, même à la
création. Un PUT identique ne modifie ni lignes, ni dates, ni audits, ni
publications. Le PUT membre ne crée aucune identité fournisseur, invitation ou
adhésion. Les droits de plateforme existants sont conservés ; aucun nouveau
droit de plateforme ne peut être attribué par cette API.

Les erreurs ont la forme `{"error":{"code":"...","message":"..."}}`.
Une collision ou réattribution d'UUID renvoie `409 conflict` sans révéler le
tenant propriétaire ; un parent absent ou étranger renvoie
`404 parent_not_found`. La dernière rétrogradation ou suspension d'un propriétaire
actif est refusée par `409 last_owner`, y compris en concurrence. Les rôles
personnalisés sont conservés. Suspendre un parent ne réécrit pas ses enfants.
Une route ou méthode non prise en charge renvoie `404 not_found`.

## Domaines et mise à jour

Le routage public utilise un domaine persistant actif et un tenant actif.
L'hôte partagé de `XOLO_HTTP_BASE_URL` est réservé et reste disponible en mode
mono-tenant, même après renommage du slug du tenant. Les URL de base et callbacks
OIDC utilisent le domaine validé et le schéma, port et préfixe configurés.
Autoriser ces callbacks dans le fournisseur d'identité.

La migration reste automatique au démarrage. Lors de la première bascule, les
hôtes existants issus de `XOLO_MULTITENANCY_HOST_PATTERN` deviennent des domaines
persistants. Ce paramètre devient facultatif et ne sert plus au routage.
Les nouveaux tenants et les changements de slug ne créent pas automatiquement
de domaines. Déclarer ceux-ci par PUT.

Sauvegarder la base, conserver `XOLO_SECRET_KEY`, arrêter les anciennes répliques,
puis démarrer la nouvelle version. Les clés API et secrets restent utilisables.
Les anciennes sessions peuvent nécessiter une reconnexion.

Les clients de provisioning doivent adopter les nouveaux chemins et corps PUT ;
les anciennes routes sont retirées, sans alias ni `/v2`. Les certificats sans
URI SAN autorisée doivent être renouvelés et la liste d'URI configurée.

## Extensions Xolo

Les fonctions propres à Xolo sont disponibles sous `/v1/xolo` :

- `GET /healthz` et `GET /permissions` ;
- `GET`/`PATCH /tenants/{tenantID}` pour les métadonnées Xolo ;
- `GET`/`PATCH /tenants/{tenantID}/organizations/{orgID}` pour les paramètres
  d'organisation, notamment description, devise et partage des quotas ;
- CRUD des rôles sous `/tenants/{tenantID}/organizations/{orgID}/roles` ;
- `PUT /tenants/{tenantID}/organizations/{orgID}/members/{membershipID}/roles`
  pour les attributions Xolo, avec l'identifiant interne d'adhésion ;
- `GET`/`PUT /tenants/{tenantID}/users` pour les identités fournisseur/sujet.

Ces extensions gardent leurs représentations spécifiques et leurs réponses de
création/suppression `201`/`204`. Elles ne font pas partie du manifeste minimal.

## Lectures, préconditions et synchronisation

Chaque chemin PUT accepte aussi GET, avec la même représentation et un en-tête
`ETag: W/"u-<unix-microseconds>"`. Ce timestamp appartient à la représentation
commune : connexion, rattachement d'identité et métadonnées propres à Xolo ne
le modifient pas. Deux changements peuvent partager la même microseconde ;
l'ETag est opaque et ne constitue pas un compteur de révision.

PUT accepte `If-Match: *` pour exiger une ressource existante, ou une liste de
tags séparés par des virgules. La comparaison ignore `W/`, conformément à la
règle particulière du contrat. Validation, comparaison et mutation partagent
le verrou transactionnel. Une condition périmée renvoie
`412 precondition_failed`, même pour un corps identique ; une syntaxe incorrecte
renvoie `400 invalid_precondition`.

Retirer la dernière clé d'un chemin unitaire donne sa collection, y compris les
ressources suspendues. Les adhésions se listent dans
`/v1/tenants/{tenantID}/organizations/{organizationID}/members`.
`limit` vaut 100 par défaut, entre 1 et 1000. La réponse contient `items`
(`key`, `representation`, `etag`) et `next_cursor`, nul à la fin.
Continuer avec `cursor` et la même limite. Le tri utilise les clés immuables
canoniques, comparées octet par octet ; aucun total ni instantané global des
pages n'est promis.

Les curseurs de liste sont authentifiés par HMAC-SHA256 et liés à l'instance,
la collection, ses parents et la taille de page. Ils expirent **24 heures après
la première page**, sans renouvellement à la continuation. Un curseur altéré
ou utilisé dans un autre périmètre renvoie `400 invalid_cursor` ; un curseur
reconnu mais expiré renvoie `410 cursor_expired`. Ils sont opaques, non chiffrés.

`GET /v1/events/cursor` renvoie `{"cursor":"..."}`, même sur un flux vide.
`GET /v1/events?cursor=...&limit=100` renvoie `items`, `next_cursor` toujours
non vide et `has_more`. Le curseur d'événements est lié à l'instance et au flux,
mais pas à la limite. `has_more=false` signifie que l'horizon de cette réponse
est rattrapé : continuer à interroger le flux pour les changements suivants.

Le profil CloudEvents 1.0 est fermé : UUID d'événement, source persistante
`urn:uuid:...`, séquence décimale sous forme de chaîne, date, request ID et
`data` contenant seulement `resource_type`, `key`, `etag`. Aucun nom, e-mail,
acteur ou attribut interne n'est publié. L'audit conserve séparément l'acteur
et les états avant/après. Un PUT effectif émet un fait ; répétition et rollback
n'en émettent aucun. Les changements locaux de rôle et de statut conservent
leurs faits distincts. Une modification propre à Xolo n'émet pas de fait commun.

Capturer C0 **avant** de lister les cinq familles, puis rejouer depuis C0 en
relisant chaque clé avec GET. Découvrir les enfants de tout nouveau tenant ou
organisation. Sérialiser lecture et application par clé, persister les données
avant le checkpoint et dédupliquer par `(source, id)`. Une lecture peut être
plus récente que son événement. Une erreur sur une référence connue, y compris
un 404 inattendu, retient le checkpoint. Une extension inconnue et indépendante
peut être signalée puis dépassée. Sur 410, reconstruire toutes les collections
dans une nouvelle génération depuis un nouveau C0 ; remplacer la génération
précédente seulement après rattrapage du flux.

### Stockage, horizon et rétention

La migration automatique `202610020002` crée `common_records` et `common_feeds`,
reprend les ressources existantes et retire les anciens snapshots internes de
la table de publication. Les audits internes restent conservés. Aucune action
manuelle ni événement de création artificiel n'est nécessaire. La source du
flux et la clé de signature des curseurs persistent dans la base, indépendamment
des redémarrages et changements d'URL ; elles font partie de la sauvegarde.

Tous les writers d'identité verrouillent `publication_clocks` avant de lire ou
modifier les parents : verrou de ligne PostgreSQL, verrou d'écriture SQLite.
Le verrou est conservé jusqu'au commit ou rollback. Le writer suivant ne peut
pas allouer ou valider une position supérieure pendant ce temps. Lecture du
flux, capture et purge utilisent ce même verrou. Le compteur validé forme donc
un horizon sûr, y compris les trous liés à l'audit interne. Ce choix limite le
débit des écritures d'identité ; aucun publisher asynchrone ni calcul fondé sur
`MAX(sequence)` n'est nécessaire.

La rétention est **illimitée par défaut**, sans purge automatique ni variable
d'environnement dans ce lot. La méthode de maintenance du store
`PurgeCommonEvents(ctx, before)` retire seulement un préfixe résolu antérieur
au seuil UTC. Elle persiste la position du dernier événement supprimé, même
après purge complète. Un retour en arrière de l'horloge ne permet pas de
supprimer un événement récent situé plus tôt dans la séquence. Un ancien
curseur dont l'historique est perdu renvoie 410, sans renouvellement implicite.
Toute future politique planifiée doit laisser assez de temps pour reconstruire.

Le store valide les parents immuables et les références publiées dans la même
transaction, y compris pour une CLI. La publication n'accepte aucun payload
fourni par l'appelant. Xolo utilise un identifiant SQL applicatif de confiance,
sans RLS PostgreSQL par tenant ni rôle SQL séparé pour la publication. SQLite
ne dispose pas de cette frontière de rôles. Le code possédant le handle GORM
brut ou un accès direct en écriture à la base reste de confiance et peut
contourner ces contrôles ; les clients provisioning ne reçoivent aucun de ces
accès. Aucune protection SQL contre un identifiant applicatif compromis n'est
revendiquée.

Les suppressions physiques des extensions Xolo restent hors du profil commun
sans suppression. Un 404 consécutif ne constitue pas un tombstone implicite ;
le protocole de cycle de vie relève d'un lot ultérieur.
