# Prompt per Progetto Software: NAS Web Management Interface (Lightweight DSM Alternative)

## Obiettivo del Progetto

Sviluppa una web interface leggera per la gestione di un sistema NAS basato su Linux, integrabile in una distribuzione ISO custom. Il sistema deve essere un'alternativa snella a Synology DSM, ottimizzata per hardware x86 Intel/AMD anche di vecchia generazione con risorse limitate (minimo 4 GB di RAM).

## Requisiti di Sistema (Vincoli Hardware)

- Architettura target: x86/x86_64 Intel e AMD, incluse CPU di vecchia generazione
- RAM minima: 4 GB (l'interfaccia e i servizi backend devono occupare meno di ~512 MB a riposo)
- Footprint ridotto: priorità assoluta a leggerezza, basso consumo di risorse e avvio rapido
- Distribuzione come immagine ISO Linux installabile (es. basata su Debian minimal, Alpine o simile)

## Stack Tecnologico Consigliato

- **Backend**: linguaggio a basso overhead (Go o Rust preferibili per binari statici e basso consumo RAM; in alternativa Python con framework leggero come FastAPI). Evitare stack pesanti (no Node.js full-stack, no Electron).
- **Frontend**: SPA leggera senza framework pesanti — preferire vanilla JS, Preact, Svelte o Alpine.js. No React/Angular per ridurre il peso del bundle.
- **Web server**: server embedded nel backend o nginx/lighttpd minimale.
- **Database**: SQLite per configurazione e gestione utenze (no database server pesanti).
- **Comunicazione**: REST API + WebSocket per operazioni in tempo reale (progress bar trasferimenti, stato RAID).

## Funzionalità Richieste

### 1. Gestione Utenze
- Creazione, modifica, eliminazione di utenti e gruppi
- Integrazione con utenti di sistema Linux (PAM / `/etc/passwd`, `/etc/group`)
- Gestione permessi e quote per utente/gruppo
- Ruoli: amministratore e utenti standard
- Cambio password e gestione sessioni sicure

### 2. Gestione Share di Rete
- Creazione e gestione di **share NFS** (configurazione `/etc/exports`)
- Creazione e gestione di **share CIFS/SMB** (configurazione Samba)
- Gestione granulare degli accessi per share: utenti/gruppi autorizzati, permessi read-only/read-write
- Abilitazione/disabilitazione share senza riavvio dei servizi
- Validazione delle configurazioni prima dell'applicazione

### 3. File Manager via Browser
- Interfaccia in stile file explorer con **struttura ad albero** navigabile
- Operazioni: copia, incolla, taglia, sposta, elimina, rinomina, crea cartella
- Upload e download di file (con supporto drag & drop)
- Visualizzazione proprietà file (dimensione, permessi, data)
- Gestione permessi POSIX via GUI (chmod/chown)
- Progress bar per operazioni lunghe
- Supporto multi-selezione

### 4. Gestione RAID via GUI
- Supporto a tutti i livelli RAID software (mdadm): RAID 0, 1, 5, 6, 10, e configurazioni JBOD/lineari
- Creazione, configurazione e gestione array RAID interamente da interfaccia grafica
- Monitoraggio stato array (healthy, degraded, rebuilding) con notifiche
- Gestione dischi: aggiunta/rimozione dischi, hot-spare, ricostruzione array
- Visualizzazione S.M.A.R.T. dei dischi
- Gestione filesystem sui volumi (creazione, formattazione, mount automatico)

### 5. Server DLNA
- Server DLNA/UPnP integrato per lo streaming di contenuti video (es. integrazione con MiniDLNA per leggerezza)
- Configurazione delle cartelle multimediali condivise via GUI
- Gestione del catalogo media e re-scan delle librerie

### 6. Localizzazione (i18n)
- Interfaccia completamente bilingue: **Italiano** e **Inglese**
- Selettore lingua nell'interfaccia con persistenza della preferenza
- Architettura i18n estendibile per futuri linguaggi (file di traduzione separati)

## Requisiti Non Funzionali

- **Performance**: l'intera UI e i servizi devono girare fluidamente sull'hardware target con 4 GB di RAM
- **Sicurezza**: autenticazione robusta, HTTPS, protezione CSRF, gestione sicura delle credenziali, principio del minimo privilegio per i servizi
- **Affidabilità**: nessuna operazione critica (RAID, share) deve poter corrompere il sistema; conferme per azioni distruttive
- **Usabilità**: interfaccia intuitiva, responsive, accessibile da qualsiasi browser moderno
- **Manutenibilità**: codice modulare, documentato, con separazione netta tra backend (orchestrazione comandi di sistema) e frontend

## Deliverable

1. Codice sorgente del backend e del frontend
2. Script/configurazione per la build dell'immagine ISO installabile
3. Documentazione di installazione, configurazione e utilizzo (IT/EN)
4. Istruzioni per il deployment e i requisiti di sistema
