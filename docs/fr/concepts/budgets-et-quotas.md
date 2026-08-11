# Budgets et quotas

Les budgets permettent de limiter les dépenses d'une organisation ou d'un membre en particulier. Une fois le plafond atteint, les nouvelles requêtes API sont bloquées.

> Les budgets s'appliquent uniquement aux fournisseurs en mode **Pay-as-you-go**. Les fournisseurs en mode abonnement disposent de leurs propres limites de plan.

## Hiérarchie des budgets

Les budgets fonctionnent à plusieurs niveaux :

| Niveau | Description |
| --- | --- |
| Organisation | Budget global de l'organisation |
| Membre | Budget individuel d'un membre |

Chaque niveau peut définir un budget **journalier**, **mensuel** et/ou **annuel**. Un champ laissé vide signifie « illimité ».

## Calcul du budget effectif

Pour chaque période, Xolo prend le **minimum** des budgets définis :

- Si l'organisation a un budget mensuel de 100 € et un membre de 50 € → le membre est limité à 50 €/mois.
- Si le membre n'a pas de budget personnel → il utilise le budget de l'organisation.

## Partage équitable du budget

L'option **Partager le budget équitablement** (dans les [Paramètres](../administration/organisation/parametres/parametre.md) de l'organisation) divise automatiquement le budget de l'organisation entre tous ses membres.

**Exemple** : une organisation avec 500 €/mois et 5 membres → chaque membre dispose de 100 €/mois.

Cette règle ne s'applique **qu'aux membres sans quota individuel défini** :

1. si un membre a un quota personnel → son quota est utilisé ;
2. sinon → le quota partagé (budget de l'org ÷ nombre de membres) est utilisé.

> Le partage est recalculé automatiquement à chaque modification du nombre de membres.

## Permissions associées

| Action | Permission requise |
| --- | --- |
| Consulter les budgets | `quota:read` |
| Modifier les budgets | `quota:write` |

Voir le tutoriel [Budget](../administration/organisation/budget/budget.md) pour la marche à suivre.
