# API de provisioning

L'API utilise un listener HTTPS dédié, distinct du proxy public `/v1/`.
Elle expose le manifeste et les cinq PUT du contrat App Covenant
`0.1.0-draft.1`. Les lectures communes, ETags, préconditions, listes,
synchronisation et webhooks seront livrés dans les lots suivants : cette
version ne constitue pas encore une implémentation complète du contrat.

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
