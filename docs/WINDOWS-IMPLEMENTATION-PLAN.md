# Plan de implementación del port para Windows

## Objetivo

Entregar un Codex Subscription Router para Windows que conserve el
comportamiento del proyecto macOS —múltiples suscripciones, routing por cuota,
hilos sticky, failover, perfil combinado, plugins/MCP y resets por cuenta— sobre
una copia local de la aplicación oficial actual, sin modificar el paquete
`OpenAI.Codex`, sin interrumpir la aplicación oficial que coordina el trabajo y
con instalación, actualización y rollback verificables.

El primer producto operativo será una aplicación **unpackaged/portable por
usuario**. MSIX es una fase posterior y opcional, con identidad propia. No es un
criterio de salida para el primer router funcional.

## Decisión de repositorio: fork, no reescritura

Se trabaja sobre un fork del repositorio original en lugar de empezar desde
cero:

```text
origin:
https://github.com/TheDaniXSX/codex-subscription-router-windows.git

upstream:
https://github.com/b-nnett/codex-subscription-router.git

rama pública releasable:
main
```

Motivos:

- el multiplexor Go, el routing, el modelo de cuentas, el ownership, la API de
  control y gran parte de los tests son portables;
- la UI inyectada expresa el comportamiento que se quiere conservar, aunque
  sus anclas minificadas deban remapearse para Windows;
- se mantiene la historia, atribución y licencia MIT del upstream;
- un remote `upstream` permite incorporar correcciones posteriores y revisar
  cada divergencia del port;
- la capa Windows puede añadirse en paralelo sin reorganizar prematuramente el
  código macOS.

Empezar de cero duplicaría el protocolo, el routing y los tests de lógica, y
haría más difícil demostrar paridad. Solo se reemplazan mecanismos ligados al
sistema operativo o al layout del bundle.

## Rutas y fronteras

| Uso | Ruta |
| --- | --- |
| Código del fork | checkout elegido por cada contribuidor |
| Fuente oficial | `InstallLocation` de `OpenAI.Codex`; solo lectura |
| Instalación router | `%LOCALAPPDATA%\Programs\Codex Subscription Router` |
| Perfil/estado/cuentas | `%LOCALAPPDATA%\Programs\Codex Subscription Router Data` |
| Staging | temporal, hermano del destino |
| Backups | `%LOCALAPPDATA%\Programs\.codex-subscription-router-backups` |
| Artefactos de desarrollo | `.artifacts\`/`dist\`; ignorados por Git |

El paquete oficial de `WindowsApps` no es un workspace ni un destino. El
checkout, staging, instalación y estado deben permanecer en raíces distintas.

## Baseline y evidencia inicial

| Campo | Valor comprobado |
| --- | --- |
| Paquete | `OpenAI.Codex_26.820.9563.0_x64__2p2nqsd0c76g0` |
| Versión Appx | `26.820.9563.0` |
| Arquitectura | `x64` |
| ASAR `package.json` | `26.820.71523` |
| `codexBuildNumber` | `7226` |
| Electron | `42.3.0` |
| `app.asar` SHA-256 | `e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a` |
| `codex.exe` SHA-256 | `799ff77125c47b0736ceb36e9b33975bb93d4162bca663730f3a4c90faf2add9` |
| `ChatGPT.exe` SHA-256 | `4ec11307b67796338d666f40c431b2804e41669576d3bc350dece8703bf4a114` |

Hallazgos que condicionan el plan:

- la app Windows es un paquete full-trust cuyo entrypoint es
  `app\ChatGPT.exe`;
- el bootstrap ya aplica `CODEX_ELECTRON_USER_DATA_PATH` antes del
  single-instance lock, por lo que el perfil puede aislarse desde el launcher;
- hay rutas de cache y logs hardcodeadas fuera de `userData`, que deben
  parchearse por separado;
- el ASAR no contiene source maps;
- las anclas minificadas del release macOS no son compatibles con este build;
  existe al menos una coincidencia textual que ahora corresponde a otra
  función, así que el recuento simple sería inseguro;
- una copia unpackaged no hereda protocolo, asociaciones, COM, menús del
  Explorador, licensing ni AUMID del paquete oficial.
- el ASAR intenta administrar el host Chrome Native Messaging
  `com.openai.codexextension`; reutilizarlo en el clon colisionaría con el
  registro oficial, por lo que sus ramas de alta y baja deben neutralizarse. El
  gate P0 compara la clave HKCU y el manifest oficial bajo
  `%LOCALAPPDATA%\OpenAI\extension` antes y después.

## Principios de ejecución

1. **Fail closed.** Versión, hash, layout, ancla o recuento inesperados detienen
   el build antes del destino.
2. **Fuente inmutable.** Se comparan hashes antes y después de construir.
3. **Construcción transaccional.** Todo se completa y verifica en staging antes
   de mover el destino.
4. **Estado externo.** Programa y datos tienen ciclos de vida separados.
5. **Paridad por evidencia.** Una función se marca soportada solo después de una
   prueba funcional, no porque el código compile o la pantalla abra.
6. **Una identidad propia.** Nunca se reutilizan `OpenAI.Codex`, publisher,
   CLSID, AUMID ni `codex://`.
7. **Sin reinicios forzados.** Build y pruebas no terminan procesos por nombre,
   no reinician Windows y no cierran la app oficial.
8. **Source-only.** CI y releases no reciben binarios ni ASAR oficiales.
9. **Cambios revisables.** Las diferencias de plataforma se aíslan en archivos
   `_windows`/`_unix`, launcher, patcher y packaging Windows.
10. **Rollback antes de activación.** Toda mutación del destino tiene una ruta
    de recuperación preparada y probada.

## Dependencias entre frentes

```text
Inventario + baseline
        |
        +----> backend/procesos Windows ----+
        |                                    |
        +----> launcher + raíz única --------+----> patcher transaccional
        |                                    |              |
        +----> mapa semántico ASAR/UI --------+              +--> installer
        |                                                   |      |
        +----> seguridad/ACL -------------------------------+      +--> smoke E2E
        |                                                          |
        +----> packaging portable ---------------------------------+
                                                                   |
                              integraciones Windows + MSIX <--------+
```

No se activa una instalación local hasta que backend, launcher, patcher,
seguridad básica e installer convergen. MSIX puede avanzar con fixtures
sintéticos, pero no bloquea el portable.

## Workstreams y ownership

| Frente | Responsabilidad | Entregables | Depende de |
| --- | --- | --- | --- |
| Inventario/compatibilidad | Fijar procedencia y anclas semánticas | script de inventario, perfil del baseline, mapa de bundles | app oficial |
| Backend Windows | Procesos, señales, rutas, secure files | archivos por plataforma, tests Go, build x64 | lógica upstream |
| Launcher | Perfil y raíz única antes de Electron | launcher, sidecar estricto, tests de quoting/self-test | contrato de paths |
| Patcher | Crear copia completa y fail-safe | `patch_windows_app.py`, metadata, staging/backup/rollback | launcher, mux, anchors |
| Installer | Preflight y operación visible | `install_windows.ps1`, transcript, dry run, rollback impreso | patcher |
| UI/paridad | Reanclar funciones y validar comportamiento | inyecciones Windows, fixtures y screenshots | mapa ASAR, control API |
| Seguridad | ACL, token, CORS, reparse points, redacción | utilidades securefs, tests negativos, threat model | paths/estado |
| Packaging | Portable y MSIX separado | scripts unpackaged, tests herméticos, MSIX opcional | app-root parcheado |
| Integraciones | CUA, Appshots, protocolo, shell | auditoría, implementación independiente, E2E | portable estable |
| CI/release | Quality gates source-only | workflow Windows, scans, release checklist | todos los anteriores |
| Qualification | Validar en el equipo real | smoke report, update/rollback, hashes post-test | build completo |

En trabajo paralelo, cada frente tiene archivos de ownership distintos. Los
cambios compartidos (`package.json`, `.gitignore`, README, compatibilidad) se
integran al final, después de revisar contratos, para evitar que dos frentes
oculten mutuamente sus cambios.

## Fases, estado y gates

Los estados son `Hecho`, `En implementación`, `Bloqueado por dependencia` y
`Pendiente`. Deben actualizarse con evidencia de tests, no por estimación.

La candidata Windows usa la versión `0.2.0`. El informe E2E versionado conserva
evidencia local/manual y puede seguir como plantilla `NOT QUALIFIED`; no es la
autoridad del tag. Una preview pública solo puede avanzar cuando CI repite sus
gates automatizados sobre el commit etiquetado y genera fuera del árbol
evidencia `AUTOMATED_GATES_PASSED` ligada a ese SHA. Una release estable exige
además E2E manual `QUALIFIED` y los gates de firma. Cada resultado local
anterior sigue siendo evidencia de desarrollo, no evidencia del tag.

### Fase 0 — Fork, inventario y arquitectura

**Estado: Hecho.**

- fork local creado, `origin` y `upstream` separados;
- rama `codex/windows-port` activa;
- paquete 26.820.9563.0 inventariado;
- hashes y estructura registrados;
- decisión portable primero y MSIX opcional adoptada;
- rutas de source, install, state, staging y backup definidas.

**Gate G0:** la fuente puede descubrirse y hashearse sin elevación ni cambios.

### Fase 1 — Backend y ciclo de procesos Windows

**Estado: Hecho en código y fixtures; pendiente de repetir G1 sobre el commit
final.**

Trabajo:

- resolver `codex.real.exe` por plataforma y mantener passthrough;
- crear app-server children sin consola visible;
- controlar herencia de handles y árbol de procesos;
- propagar cancelación y cerrar solo hijos del router;
- hacer escrituras de estado y credenciales con semántica Windows y ACL de
  usuario;
- mantener `%USERPROFILE%\.codex` como Primary y homes secundarios aislados.

**Gate G1:**

```powershell
go test ./...
go vet ./...
go build -trimpath -o .artifacts\codex-mux.exe .\cmd\codex-mux
```

Además, el test de proceso confirma que cerrar el mux no termina la app oficial
y no deja children huérfanos.

### Fase 2 — Launcher y contrato de raíz única

**Estado: Hecho en código y tests herméticos; pendiente de repetir G2 sobre el
commit final.**

Trabajo:

- sustituir el entrypoint copiado y preservar `ChatGPT.real.exe`;
- persistir `-StateRoot` en
  `resources\codex-router\launcher-config.json` con JSON estricto y ruta
  absoluta; el esquema exacto es
  `{"schemaVersion":1,"stateRoot":"D:\\ruta\\absoluta"}` y rechaza
  versiones o campos desconocidos;
- resolver root con la precedencia documentada y normalizarla una vez;
- establecer perfil, mux root y ruta al codex real sin tocar `CODEX_HOME`;
- preservar argumentos/deep links con quoting Windows correcto;
- rechazar un `--user-data-dir` externo;
- ofrecer un self-test sin escritura ni spawn.

**Gate G2:** tests unitarios de parsing/quoting/path traversal y prueba de
proceso con rutas que contengan espacios y caracteres no ASCII. Oficial y
router adquieren instancias únicas separadas.

### Fase 3 — Patcher Windows y aislamiento completo

**Estado: Hecho para el baseline bloqueado; dry run e instalación privada
verificados durante desarrollo. G3 debe repetirse sobre el commit final.**

Trabajo:

- autodetectar package-root o aceptar `--source`;
- normalizar `app\` a app-root;
- comprobar baseline y anclas semánticas;
- extraer/reempaquetar ASAR preservando módulos nativos;
- reanclar todas las inyecciones de cuenta/perfil/plugins/resets/thread;
- redirigir las rutas de cache y las dos de logs mediante
  `CODEX_MUX_HOME`, sin cambiar `LOCALAPPDATA` global: `bin` y `runtimes`
  comparten un único helper auditado y los dos bundles de logging tienen un
  ancla cada uno;
- parchear una sola vez `owl-app.ini` para que el nombre de user-data del
  runtime no siga siendo `Codex`;
- instalar launcher y mux, preservando binarios reales;
- escribir sidecar y `codex-mux-build.json`;
- staging hermano, backup, commit y rollback automático;
- `--dry-run` sin mutaciones.

**Gate G3:** fixtures negativas para hash, ancla ausente, ancla duplicada,
sidecar inválido, ASAR corrupto y fallo durante commit. Los hashes oficiales
pre/post son idénticos y nunca aparece un destino parcial.

### Fase 4 — Installer y lifecycle

**Estado: En implementación para la candidata 0.2.0.** La instalación
transaccional está operativa; actualización, rollback, desinstalación,
retención y limpieza deben quedar cubiertos por operaciones y tests de producto
antes de aprobar G4.

Trabajo completado en `scripts/install_windows.ps1`:

- prerequisitos y versiones;
- detección Appx;
- rutas no solapadas;
- mutex global por usuario para serializar destinos, estado y shortcut;
- creación de `StateRoot` con DACL protegida, sin herencia y únicamente usuario
  actual + `SYSTEM` con `FullControl`, antes de transcript/token;
- transcript visible;
- `npm ci` bloqueado por lockfile;
- dry run, `-NoLaunch`, `-Force` y overrides explícitos;
- Python `-X utf8 -u` para encoding determinista y progreso no bufferizado;
- self-test post-verify sin overrides, que exige sidecar y root exactos;
- detección de router abierto sin terminarlo;
- verificación post-install y comandos de rollback.

**Gate G4:** instalación limpia, actualización, fallo inyectado y rollback en
directorios con espacios. `-StateRoot` personalizado debe sobrevivir al próximo
arranque a través del sidecar. Dos installers concurrentes deben serializarse y
el dry run sobre un destino existente no debe crear backup ni mutarlo.

### Fase 5 — Paridad funcional de UI y routing

**Estado: En implementación y requalification.** Las inyecciones y contratos
principales están remapeados; gestión completa de cuentas, recuperación de
children, historial determinista y failover deben aprobar tests y E2E sobre el
ASAR real del commit final.

Orden de activación:

1. passthrough con una cuenta;
2. dos children aislados;
3. add/login/logout/enable/label;
4. nuevo hilo y ownership sticky;
5. merged history;
6. cuotas agregadas y actualizaciones;
7. failover preventivo/reactivo y all-depleted;
8. perfil combinado;
9. selector Apps/MCP;
10. resets por cuenta y atribución visible.

**Gate G5:** matriz P0/P1 completada con dos cuentas de prueba. Ningún test de
resets consume crédito real salvo una ejecución deliberada y autorizada.

### Fase 6 — Integraciones Windows

**Estado: Implementado parcialmente; degradado/experimental hasta
qualification.**

- Appshots está default-off y conserva un gate explícito; captura e inserción
  multi-monitor/DPI siguen pendientes;
- notificaciones bajo identidad propia siguen pendientes de evidencia;
- Computer Use conserva helpers y contratos estáticos; click/escritura y
  auditoría de procesos siguen pendientes;
- existe conector Chrome independiente con extensión/host propios, pero sigue
  opt-in y no es paridad soportada hasta publicación de la extensión, firma y
  E2E en VM; el clon nunca muta `com.openai.codexextension`;
- `codex-router://` y los comandos Explorer tienen gestor unpackaged propio y
  desinstalación compare-and-delete; single-instance, entradas hostiles y E2E
  Explorer siguen pendientes;
- MSIX conserva identidad, protocolo, verbos y CLSID exclusivamente propios.

**Gate G6:** cada integración se clasifica `soportada`, `degradada` o
`pendiente`. No se reutilizan registros oficiales. Computer Use requiere una
prueba real de click/escritura y auditoría de procesos; no se presume por el
arranque del renderer. Chrome/native messaging solo supera el gate con una
extensión propia, host/manifest/`allowed_origins` propios, registro por usuario
y uninstall ownership-safe; hasta entonces se reporta `gap/no parity`.

### Fase 7 — Packaging y distribución local

**Estado: Portable implementado con tests herméticos; MSIX opcional.**

- `Build-Unpackaged.ps1` normaliza package-tree y app-root;
- `router-package.json` conserva launch target y hashes;
- output existente se rechaza o se respalda, nunca se borra silenciosamente;
- MSIX usa `CodexSubscriptionRouter.Local`, publisher propio, app ID `Router`,
  protocolo `codex-router` y solo `runFullTrust` inicialmente;
- assets y certificado se proporcionan de forma explícita.

**Gate G7 portable:** `Test-Packaging.ps1` pasa sin binarios oficiales y el
artefacto local probado lanza desde su ruta instalada.

**Gate G7 MSIX:** firma, registro, launch, upgrade y uninstall en VM limpia. No
bloquea el release portable.

### Fase 8 — Qualification en el equipo real

**Estado: Pendiente del commit final.** Los ensayos locales previos no se
promueven automáticamente a evidencia de release.

Secuencia segura:

1. ejecutar suite source-only;
2. ejecutar inventario y `-DryRun`;
3. instalar con `-NoLaunch` para no interrumpir la app oficial;
4. verificar metadata, hashes, launcher self-test y layout;
5. abrir el router como proceso independiente;
6. completar smoke test de una cuenta y después de dos;
7. probar actualización con `-Force -NoLaunch`;
8. probar rollback al backup anterior;
9. confirmar hashes y funcionamiento de la app oficial;
10. guardar un informe redacted, sin tokens, códigos de dispositivo ni emails.

**Gate G8:** informe E2E del commit exacto y baseline exacto, sin desviaciones
P0 abiertas.

### Fase 9 — Release/operación

**Estado: En implementación.** La metadata 0.2.0 y el gate Windows están
preparados; el tag continúa bloqueado hasta G8 y el pipeline source-only final.

- source-only release;
- changelog, VERSION y matriz de compatibilidad coherentes;
- CI Windows/Linux/macOS para código propio;
- secret scan y forbidden-artifact scan;
- SBOM del proyecto;
- tag inmutable y rollback target documentado;
- proceso de incorporar nuevos baselines sin wildcard.

**Gate G9:** CI verde, qualification redacted aprobada y ninguna copia de
OpenAI en Git, caché CI o attachments.

## Plan de pruebas

### Nivel 1: estáticas y unitarias

- `gofmt`/`go test`/`go vet` para backend;
- tests por plataforma para nombres, señales, environment y ACL;
- sintaxis JS de todas las inyecciones;
- `py_compile` y tests del patcher;
- análisis PowerShell y tests herméticos del installer/packaging;
- build launcher con warnings como errores.

### Nivel 2: contratos con fixtures sintéticos

- ASAR mínimo con cada ancla correcta;
- ancla ausente, duplicada y semánticamente equivocada;
- package-root y app-root;
- rutas con espacios, Unicode y longitud elevada;
- symlink/junction/reparse point en staging/destino;
- sidecar desconocido, relativo, con campos extra o JSON inválido;
- simulación de fallo antes y después del movimiento del destino.

Estos tests no necesitan ni contienen binarios oficiales y son aptos para CI.

### Nivel 3: integración del backend

- fake app-server por cuenta;
- IDs JSON-RPC numéricos y string concurrentes;
- initialize/initialized y child tardío;
- routing golden fixtures;
- ownership tras restart;
- failover preventivo/reactivo, máximo una visita por cuenta;
- all-depleted y reset conocido;
- cierre/cancelación sin proceso huérfano.

### Nivel 4: build local del baseline

- verificar firma/identidad del input oficial antes de copiar;
- hashes pre/post;
- ASAR unpack/repack y módulos nativos;
- metadata de procedencia;
- launcher self-test;
- verificador de layout y escaneo de rutas oficiales hardcodeadas restantes.

### Nivel 5: E2E funcional

- oficial y router simultáneos;
- Primary coincide con la cuenta actual;
- login de una cuenta secundaria;
- aislamiento de auth, SQLite y MCP OAuth;
- routing, sticky, merged history y failover;
- perfil combinado, plugins y resets;
- Appshots/Computer Use cuando su gate esté habilitado;
- comprobación de que el router no registra ni elimina
  `com.openai.codexextension`, incluyendo hashes/valores pre/post de
  `HKCU\Software\Google\Chrome\NativeMessagingHosts\com.openai.codexextension`
  y `%LOCALAPPDATA%\OpenAI\extension\com.openai.codexextension.json`; un futuro
  conector propio requiere E2E separado;
- escalado 100/125/150/200 %, teclado y accesibilidad.

### Nivel 6: lifecycle y recuperación

- fresh install;
- rebuild misma versión;
- update de router;
- rechazo de baseline oficial desconocido;
- router abierto: activación rechazada sin terminarlo;
- rollback automático por fallo inyectado;
- rollback manual y conservación de state;
- desinstalación recuperable por move, no delete.

## Estrategia DevOps

### Integración de upstream

```powershell
git fetch upstream --prune
git log --oneline --left-right --cherry-pick codex/windows-port...upstream/main
```

Cada sincronización se hace en una rama temporal, con revisión específica de
mux, UI y patcher. No se hace merge ciego de `scripts/patch_app.py` sobre el
perfil Windows.

### CI

La CI pública solo compila y prueba código del proyecto. Usa fixtures
sintéticos, acciones fijadas por commit, permisos mínimos, npm lockfile y Go
modules verificados. Debe fallar si detecta `.exe`, `.dll`, `.asar`, `.msix`,
tokens, `auth.json`, certificados o dumps no permitidos.

La qualification que necesita el paquete oficial se ejecuta localmente y solo
publica resultados redactados.

### Versionado

Se mantienen separados:

- versión SemVer del router;
- versión completa del Appx oficial;
- perfil de patch Windows para ese baseline;
- versión de esquema del estado.

Añadir compatibilidad con una nueva versión oficial no cambia silenciosamente
el perfil anterior. Los metadatos permiten saber exactamente qué combinación
produjo una instalación.

### Observabilidad

- transcript por instalación;
- logs bajo la raíz del router, no la oficial;
- mensajes con fase, ruta relativa y exit code;
- redacción de tokens, device codes, headers y email completo;
- eventos de routing con account ID/label y score, nunca contenido del prompt.

## Plan de actualización y rollback

### Update de código del router

1. suite CI/local;
2. build en staging mientras la app activa sigue funcionando;
3. detectar router abierto antes de activar;
4. cerrar solo el router de forma manual;
5. `-Force -NoLaunch`;
6. verificar metadata y smoke test;
7. conservar al menos el backup anterior.

### Update de la app oficial

1. dejar el router validado anterior operativo;
2. inventariar el nuevo Appx;
3. crear perfil de compatibilidad nuevo;
4. reanclar semánticamente y ejecutar negativos;
5. construir como versión separada;
6. activar solo tras qualification.

### Rollback

El rollback primario es de programa: mover la instalación fallida a cuarentena
y restaurar el directorio versionado bajo
`.codex-subscription-router-backups`. La raíz de estado no se toca.

Si existe una migración de esquema, se conserva primero el estado fallido, se
valida checksum/esquema del backup y se restaura mediante reemplazo atómico. No
se copian `auth.json` entre cuentas ni se borra evidencia antes de diagnosticar.

## Definition of Done

El router se considera **funcionando** para este equipo solo cuando:

- G0–G5 y G8 están aprobados;
- instala desde el fork con un único comando y `-NoLaunch`;
- la app oficial conserva hashes y puede seguir abierta;
- dos suscripciones diferentes están conectadas y aisladas;
- un hilo nuevo se asigna, conserva ownership y puede hacer failover;
- cuota agregada, perfil, plugins/MCP y resets están verificados;
- caches, logs y perfil no escriben en las ubicaciones oficiales;
- update y rollback han sido ejercitados;
- no hay procesos huérfanos ni credenciales en logs;
- las limitaciones de Appshots, Computer Use, Chrome/native messaging,
  protocolo y MSIX están declaradas con evidencia, no ocultas;
- ninguna operación del router crea, reemplaza o elimina el host oficial
  `com.openai.codexextension`.

Hasta completar esos puntos, un build que únicamente arranca es un prototipo,
no un router terminado.
