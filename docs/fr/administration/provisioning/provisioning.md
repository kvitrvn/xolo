# API de provisioning

L'API utilise un listener HTTPS dédié, distinct du proxy public `/v1/`.
Elle expose le manifeste et les cinq PUT du contrat App Covenant
`0.1.0-draft.1`, ainsi que les lectures communes, ETags, préconditions, listes
et flux de synchronisation des lots 1 à 3. Le lot 4 ajoute les webhooks durables
facultatifs ; cette version ne revendique pas une conformité complète au contrat.

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

## Webhooks durables — extension Xolo

Les webhooks restent des notifications facultatives. Le consommateur interroge
`/v1/events` au démarrage, à la reconnexion et périodiquement ; une notification
ne fait jamais avancer son checkpoint du flux. La livraison est au moins une
fois. Le destinataire vérifie la signature avant de décoder le JSON, accepte
un écart d’horloge maximal de 300 secondes dans les deux sens, déduplique
`(source, id)` durablement et accuse l’acceptation durable avec un statut 2xx.

La migration `202610020003` est automatique au démarrage. Une installation
existante n’a aucune commande de migration à exécuter. Les webhooks restent
désactivés par défaut ; leur activation demande une destination explicitement
autorisée et un abonnement. Le worker fonctionne indépendamment du listener
provisioning. Le CRUD utilise ce listener mTLS lorsque les deux fonctions sont
activées. `GET /v1/xolo/extensions` annonce séparément l’extension ; le manifeste
commun reste inchangé.

| Variable | Défaut | Description |
| --- | --- | --- |
| `XOLO_WEBHOOKS_ENABLED` | `false` | Active matérialisation, livraison et nettoyage |
| `XOLO_WEBHOOKS_ALLOWED_ORIGINS` | obligatoire si activé | Origines HTTPS exactes séparées par des virgules, avec port non standard éventuel |
| `XOLO_WEBHOOKS_ALLOW_PRIVATE_NETWORKS` | `false` | Autorise les adresses privées et loopback des origines permises |
| `XOLO_WEBHOOKS_TLS_CA_FILE` | vide | CA PEM supplémentaire, en complément des racines système |
| `XOLO_WEBHOOKS_WORKERS` | `2` | Livraisons simultanées par processus, entre 1 et 16 |
| `XOLO_WEBHOOKS_POLL_INTERVAL` | `1s` | Intervalle de polling, entre 100 ms et 1 minute |
| `XOLO_WEBHOOKS_QUEUE_CAPACITY` | `10000` | Lignes de livraison conservées, entre 1 et 1 000 000 |

Les origines n’acceptent ni joker, chemin, credentials, query string ou fragment.
Une destination peut ajouter un chemin, mais pas de query string, fragment ou
credentials. Chaque réponse DNS est vérifiée à la connexion ; l’adresse IP
contrôlée est ensuite utilisée directement. Les proxies HTTP de l’environnement
sont ignorés. Les adresses link-local, multicast, non spécifiées, partagées ou
réservées restent interdites, notamment celles des métadonnées cloud, même
avec l’option réseau privé. Les certificats TLS sont toujours vérifiés.
Les restrictions réseau sortantes du déploiement doivent aussi couvrir Xolo.

Chaque abonnement possède un UUID fourni par le client et appartient à un
tenant. La propriété est `instance` ; son transfert relève du lot d’adoption.
La suspension du tenant ne suspend pas les notifications de contrôle. Le
parent est résolu avant lecture des credentials. L’UUID ne peut pas être
réaffecté à un autre tenant. La limite est de 100 abonnements par instance.

| Méthode et route | Comportement |
| --- | --- |
| `GET /v1/xolo/tenants/{tenantID}/webhooks` | Liste sans secrets |
| `GET /v1/xolo/tenants/{tenantID}/webhooks/{id}` | Paramètres, position, état et nombre de clés |
| `PUT /v1/xolo/tenants/{tenantID}/webhooks/{id}` | Création ou remplacement des paramètres, statut 200 |
| `DELETE /v1/xolo/tenants/{tenantID}/webhooks/{id}` | Suppression de l’abonnement, credentials et toutes ses livraisons, statut 204 |
| `GET /v1/xolo/tenants/{tenantID}/webhooks/{id}/deliveries` | Les 100 derniers diagnostics, sans payload ni credentials |
| `POST /v1/xolo/tenants/{tenantID}/webhooks/{id}/reset` | Reconnaît la perte, vide la file et repart de l’horizon courant |
| `GET /v1/xolo/webhooks/status` | Compteurs de file, retards et pertes d’historique |

PUT exige `destination`, `events` et le booléen `enabled`. `events` contient
`["*"]` ou une liste non vide de types exacts du flux commun. `secrets` est
obligatoire à la création : une ou deux clés distinctes en base64, éventuellement
préfixées `whsec_`, décodant chacune 32 à 64 octets. Générer les clés hors de Xolo.
Omettre `secrets` lors d’une mise à jour conserve les clés actuelles. Elles sont
en écriture seule, chiffrées en AES-GCM avec `XOLO_SECRET_KEY` et liées dans le
contenu chiffré au tenant et à l’abonnement. Sauvegarder cette clé avec la base.

Pour une rotation, envoyer `[ancienne, nouvelle]`, faire accepter les deux au
destinataire, puis envoyer `[nouvelle]` après sa bascule. La réponse n’expose
que `secret_count`. Chaque tentative utilise les paramètres actuels : rotation
et changement de destination s’appliquent aussi aux livraisons en attente. Un
changement de filtre ne concerne que les événements non encore matérialisés.
`enabled=false` arrête les nouvelles matérialisations et réservations sans
avancer la position ni supprimer le travail. Une requête déjà réservée peut
encore terminer après désactivation, reset ou suppression ; son appel est
borné et un ancien résultat ne peut pas écraser une réservation plus récente.

Un nouvel abonnement part de l’horizon sûr courant, sans rejouer l’historique.
La matérialisation lit le flux sous son verrou d’allocation, puis insère les
livraisons et avance la position dans la même transaction. La paire unique
abonnement/événement évite les doublons de file. Chaque transaction lit au plus
100 publications, tous abonnements confondus, en priorisant les positions les
plus anciennes pour borner le verrou et éviter la famine. Les réservations expirent après
30 secondes et portent un jeton de réservation renouvelé. L’appel HTTP est hors
transaction. Un crash après acceptation mais avant enregistrement peut donc
redélivrer le même événement avec le même ID.

Le POST HTTPS envoie les octets exacts du CloudEvent conservé avec
`application/cloudevents+json`. `webhook-id` contient son UUID et
`webhook-timestamp` les secondes Unix de la tentative. HMAC-SHA256 signe
`id.timestamp.body` avec les octets de la clé décodée ; `webhook-signature`
contient une signature `v1,<base64>` par clé active, séparées par des espaces.
Une reprise conserve ID et corps, renouvelle timestamp et signatures, sans
ajouter d’acteur, e-mail ou attribut au profil commun.

Le client borne DNS/connexion à 3 secondes, TLS et en-têtes à 5 secondes, et
l’ensemble de l’appel à 10 secondes, lecture comprise. Il refuse les
redirections, limite les en-têtes à 16 Kio et le corps de réponse à 64 Kio,
et respecte l’annulation. Seule une réponse 2xx entièrement lue et bornée
réussit. Les diagnostics sont des codes fixes (`transport_error`, `redirect`,
`http_status`, `response_too_large`, `credentials_unavailable`…), sans corps
de réponse, signature, secret ou détail d’erreur réseau.

Les reprises attendent 5, 10, 20, 40… secondes, avec plafond d’une heure et
limite de 12 réservations ou 24 heures depuis la matérialisation. Une réservation
expirée compte comme tentative. Après épuisement, la livraison devient `failed` :
examiner le diagnostic et réconcilier par le flux. Les succès et échecs terminaux,
avec leurs copies indépendantes des payloads, restent sept jours après leur
fin, puis sont nettoyés lorsque le worker fonctionne. Un payload matérialisé
survit à la purge du flux. La rétention de celui-ci reste illimitée par défaut.

La capacité compte **toutes** les lignes, y compris les succès et échecs conservés.
Une file pleine suspend la matérialisation avec l’état `backpressure`, sans
perdre sa position ni bloquer les écritures de ressources. Prévoir le produit
capacité × taille des payloads/lignes/index, plus le flux et l’audit indépendants.
10 000 lignes représentent habituellement des dizaines de Mio, sans constituer
un quota disque en octets : mesurer la base et surveiller l’espace libre.
Diminuer la capacité ne supprime aucune livraison existante.

Une position située avant la borne de rétention passe à `history_lost` sans
avance implicite. Les payloads déjà matérialisés peuvent encore être livrés.
Reconstruire le consommateur depuis un nouveau C0, puis effectuer un reset
explicite avec `{"acknowledge_loss":true}` ; continuer le polling depuis le
checkpoint propre au consommateur. Le reset efface toutes les livraisons de
l’abonnement et repart de l’horizon courant. La suppression d’un tenant retire
aussi ses abonnements et livraisons dans la même transaction. Le cycle de vie
des organisations et membres reste hors du profil commun sans suppression ;
les clés tenant/abonnement préparent le nettoyage par périmètre des lots suivants.

À configuration par défaut, l’objectif est une première tentative en quelques
secondes lorsque le système est sain, sans SLA de débit ou de latence.
`xolo_webhook_attempts_total` et `xolo_webhook_failures_total{reason}` sont des
compteurs par processus. `xolo_webhook_queue{state}`,
`xolo_webhook_lag_seconds{stage}` et `xolo_webhook_history_lost` sont des jauges
pour toute la base : utiliser le maximum entre réplicas, pas la somme.
Les labels ne contiennent aucun tenant, URI ou ID d’événement. Alerter sur
retard durable, échecs, saturation ou perte d’historique. La publication commune
est synchrone avec le commit ; le retard de matérialisation mesure les événements
validés qui ne sont pas encore mis en file.

L’arrêt annule les appels HTTP et attend les workers. L’enregistrement du
résultat dispose de cinq secondes supplémentaires ; en cas de crash ou de base
indisponible, la réservation devient récupérable à expiration. Prévoir au moins
15 secondes de grâce pour le processus. Une console inaccessible provoque des
reprises, sans arrêt du serveur. Désactiver le worker conserve les abonnements
et le travail ; le nettoyage reprend à sa réactivation.
