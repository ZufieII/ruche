# Ruche

Alternative libre et auto-hébergeable à Hive OS pour piloter des rigs de minage.
(« Ruche » est un nom de travail : « Hive OS » est une marque de Hiveon.)

## Ce que fait la version 0.1

- **Tableau de bord web** : rigs en ligne ou hors ligne, hashrate total et par GPU, températures, ventilateurs, consommation, courbe du hashrate sur 6 h.
- **Flight sheets** : algorithme, pool, wallet, worker. On l'assigne à un rig et le mineur démarre tout seul.
- **Commandes à distance** : relancer ou arrêter le mineur, redémarrer le rig.
- **Alertes** (Telegram ou logs) : rig hors ligne, retour en ligne, GPU trop chaud.
- **Agent du rig** : lit les GPU NVIDIA (`nvidia-smi`) et AMD (sysfs), lance lolMiner, relance le mineur s'il plante.
- L'agent se connecte **vers** le serveur : aucun port à ouvrir sur le rig.
- Le serveur ne garde qu'une empreinte des tokens des rigs.

## Architecture

```
rig (Linux + pilotes GPU)                serveur (Docker)
┌──────────────────────┐   HTTPS   ┌──────────────────────────┐
│ ruche-agent          │ ────────► │ ruche-server             │
│  ├ stats GPU         │  rapport  │  ├ API + tableau de bord │
│  └ lolMiner          │ ◄──────── │  ├ SQLite                │
└──────────────────────┘  ordres   │  └ alertes Telegram      │
                                   └──────────────────────────┘
```

## Démarrer le serveur

```bash
cd deploy
cp .env.example .env      # puis choisis un vrai mot de passe
docker compose up -d
```

Sans Docker : `RUCHE_ADMIN_PASSWORD=... go run ./cmd/ruche-server`.
Ouvre http://localhost:8080 (utilisateur au choix, mot de passe de `.env`).
En dehors de ton réseau local, mets-le derrière un reverse proxy HTTPS (Caddy par exemple).

## Ajouter un rig

1. Dans le tableau de bord, ajoute un rig : il affiche un fichier `rig.conf` à copier.
2. Sur le rig (Debian ou Ubuntu avec les pilotes GPU installés) :
   ```bash
   GOOS=linux GOARCH=amd64 go build -o ruche-agent ./cmd/ruche-agent   # sur ta machine
   sudo ./deploy/install-agent.sh ./ruche-agent ./rig.conf               # sur le rig
   ```
3. Crée une flight sheet et assigne-la au rig.

## Essayer sans matériel

```bash
go build -o bin/ ./cmd/...
RUCHE_ADMIN_PASSWORD=demo12345 ./bin/ruche-server &
# crée un rig dans le tableau de bord, colle son rig.conf dans demo.conf, puis :
./bin/ruche-agent -config demo.conf -fake-gpus 4
```

## Tests

```bash
go test ./...
```

## Pas encore fait

- Image Linux prête à flasher (pour l'instant on installe l'agent sur un Debian/Ubuntu existant)
- Overclocking et undervolt
- Autres mineurs que lolMiner, ASIC
- Plusieurs utilisateurs, 2FA
- Calcul de rentabilité avec le prix de l'électricité
