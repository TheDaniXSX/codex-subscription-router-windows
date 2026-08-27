# Instalación en Windows

## Resultado de la instalación

El instalador construye para el usuario actual una copia independiente de
Codex Subscription Router en:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router
```

El estado, el perfil y las cuentas quedan fuera del programa, en:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router Data
```

La aplicación oficial de Codex permanece registrada, actualizable y sin
cambios. El instalador no la cierra, no la desregistra, no cambia permisos de
`WindowsApps` y no escribe allí. Es posible construir el router mientras la app
oficial está abierta; solo hay que cerrar una instalación **anterior del
router** cuando se usa `-Force`.

> [!IMPORTANT]
> El artefacto generado contiene binarios copiados de la instalación oficial
> del equipo. No debe subirse a Git, adjuntarse a una release ni redistribuirse.
> El repositorio publica únicamente código fuente y herramientas.

## Compatibilidad inicial

El baseline aprobado es:

| Componente | Valor |
| --- | --- |
| Paquete | `OpenAI.Codex` |
| Versión | `26.820.9563.0` |
| Arquitectura | `x64` |
| `app.asar` SHA-256 | `e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a` |

El instalador selecciona de forma predeterminada el paquete
`OpenAI.Codex` más reciente registrado para el usuario. Si Microsoft Store ya
lo ha actualizado a otra versión, el parcheador se detendrá antes de instalar
nada. No use `-AllowUntestedSource` como solución rutinaria: primero hay que
revisar el nuevo ASAR, actualizar las anclas y completar las pruebas.

## Requisitos

- Windows 10 2004 o posterior; el baseline se obtuvo en Windows x64.
- La aplicación oficial de Codex instalada para el usuario actual.
- PowerShell 5.1 o PowerShell 7.
- Python 3.10 o posterior, disponible como `python.exe` o `py.exe`.
- Node.js 22.12 o posterior, con npm.
- Go 1.26 o posterior, salvo que se proporcionen **ambos** ejecutables
  precompilados mediante `-MuxPath` y `-LauncherPath`.
- Un clone local de este repositorio.

El formato portable soportado no necesita Windows SDK, certificado de firma ni
privilegios de administrador. `makeappx.exe` y `signtool.exe` solo son
relevantes para la vía MSIX experimental.

Compruebe el entorno desde PowerShell:

```powershell
python --version
node --version
npm --version
go version
Get-AppxPackage -Name OpenAI.Codex |
    Select-Object Name, Version, Architecture, InstallLocation
```

El script vuelve a comprobar las versiones y muestra una explicación si falta
una herramienta. No lo ejecute como administrador: la instalación y el estado
son deliberadamente por usuario.

En una instalación real, el script crea `StateRoot` y protege su DACL **antes**
de abrir el transcript o crear el token. Deshabilita la herencia y concede
`FullControl` únicamente al SID del usuario actual y a `SYSTEM`. Si no puede
aplicar y volver a verificar esa ACL, se detiene antes de escribir datos
sensibles.

## Instalación recomendada

Abra PowerShell en la raíz del repositorio y haga primero una validación sin
publicar la app:

```powershell
npm ci --ignore-scripts --no-audit --no-fund
pwsh -NoProfile -File .\scripts\install_windows.ps1 -DryRun
```

`-DryRun` descubre el paquete, comprueba rutas y prerequisitos, toma hashes y
ejecuta el modo de validación de solo lectura del parcheador. No instala ni
lanza el router. Como un dry run no debe modificar tampoco el checkout,
`node_modules` debe existir de antemano; por eso se ejecuta `npm ci` de forma
explícita en el ejemplo. Si el destino ya existe, el instalador pasa `--force`
al patcher únicamente para permitir el diagnóstico: `--dry-run` sigue evitando
backup, publicación y cambios de estado.

Cuando la validación termine correctamente:

```powershell
pwsh -NoProfile -File .\scripts\install_windows.ps1 -NoLaunch
```

`-NoLaunch` es recomendable cuando hay una tarea activa en la aplicación
oficial: construye y verifica el router sin cerrar, reiniciar ni sustituir la
app desde la que se está trabajando. Para abrirlo después:

```powershell
Start-Process "$env:LOCALAPPDATA\Programs\Codex Subscription Router\ChatGPT.exe"
```

Si se desea abrirlo automáticamente al terminar, omita `-NoLaunch`:

```powershell
pwsh -NoProfile -File .\scripts\install_windows.ps1
```

La instalación normal ejecuta `npm ci` con el lockfile, compila el
multiplexor y el launcher, construye el árbol en *staging*, verifica su
procedencia, lo mueve al destino y crea un acceso directo por usuario en el menú
Inicio. No reclama el protocolo oficial `codex://`.

Todas las instalaciones del router en la misma sesión de usuario comparten un
mutex, incluso si usan destinos o `StateRoot` diferentes. Esto serializa los
recursos comunes —estado, backups y acceso directo— y evita carreras entre dos
instaladores. El patcher se ejecuta con Python `-X utf8 -u`: UTF-8 explícito y
salida sin buffer para que el transcript muestre progreso inmediato.

La integración de escritorio unpackaged es independiente y opt-in. Tras
instalar, se puede previsualizar, registrar o retirar el protocolo privado
`codex-router://` y los verbos clásicos de Explorer con
`scripts/windows/Manage-ShellIntegration.ps1`. Admite `-WhatIf`, escribe solo
bajo `HKCU` y no toma posesión de `codex://`. Consulte
[WINDOWS-SHELL-INTEGRATION.md](WINDOWS-SHELL-INTEGRATION.md) para el contrato de
URI, las claves exactas y la desactivación compare-and-delete.

## Qué se crea

El layout soportado es portable y tiene como raíz el destino:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router\
├── ChatGPT.exe                 launcher independiente
├── ChatGPT.real.exe            escritorio oficial copiado
├── resources\
│   ├── app.asar                renderer parcheado
│   ├── codex.exe               codex-mux.exe
│   ├── codex.real.exe          CLI/app-server oficial copiado
│   ├── codex-router\
│   │   └── launcher-config.json raíz de estado absoluta
│   └── ...
├── codex-mux-build.json        procedencia y hashes
└── ... runtime copiado ...
```

Los datos persistentes se separan del programa:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router Data\
├── Profile\                    perfil Chromium/Electron
├── control-token
├── state.json
├── accounts\
│   └── <id>\codex-home\
├── runtime-cache\
│   ├── bin\
│   └── runtimes\
└── logs\
    └── install-<timestamp>.log
```

El acceso directo se crea de forma atómica en:

```text
%APPDATA%\Microsoft\Windows\Start Menu\Programs\Codex Subscription Router.lnk
```

La cuenta primaria sigue usando `CODEX_HOME` o, por defecto,
`%USERPROFILE%\.codex`. El launcher no cambia `CODEX_HOME` ni
`CODEX_SQLITE_HOME`; únicamente aísla el perfil del escritorio y la raíz del
multiplexor. Las cuentas adicionales usan homes separados bajo `accounts`.
`-StateRoot` se persiste como una ruta absoluta en el sidecar estricto del
launcher, por lo que sigue vigente en lanzamientos futuros y no depende del
entorno temporal del instalador.
El parche ASAR redirige también a `runtime-cache` y `logs` las rutas Windows
que el upstream tenía hardcodeadas. No establezca un `LOCALAPPDATA` alternativo
para conseguir ese aislamiento.

## Parámetros del instalador

`scripts/install_windows.ps1` acepta:

| Parámetro | Uso |
| --- | --- |
| `-Source <ruta>` | Fuerza una raíz de paquete o árbol oficial concreto. Alias: `-SourceApp`. |
| `-Destination <ruta>` | Cambia el directorio de programa dentro de `LOCALAPPDATA`. Debe estar separado de fuente y estado. |
| `-StateRoot <ruta>` | Cambia la raíz persistente dentro de `LOCALAPPDATA` para perfil, cuentas, token y logs. |
| `-MuxPath <exe>` | Usa un multiplexor precompilado. Go solo deja de ser necesario si también se indica `-LauncherPath`. |
| `-LauncherPath <exe>` | Usa un launcher precompilado. Go solo deja de ser necesario si también se indica `-MuxPath`. |
| `-Force` | Reemplaza un router existente después de crear un backup recuperable. |
| `-DryRun` | Valida sin instalar, crear estado, acceso directo ni lanzar. |
| `-AllowUntestedSource` | Permite un baseline no listado; las anclas siguen siendo obligatorias. |
| `-SkipDependencyInstall` | Omite `npm ci` cuando `node_modules` ya corresponde al lockfile. |
| `-NoLaunch` | Instala y verifica sin abrir el router. |
| `-NoShortcut` | No crea ni actualiza el acceso directo del menú Inicio. |
| `-BackupRetention <n>` | Conserva `n` builds de rollback autenticados; por defecto, uno. |
| `-MinimumFreeBytes <n>` | Exige un suelo adicional de espacio libre además del cálculo automático de staging. |
| `-ControlPort <puerto>` | Override avanzado 49152..65535; cero genera y reserva un puerto aleatorio seguro. |

Ejemplo con rutas explícitas:

```powershell
pwsh -NoProfile -File .\scripts\install_windows.ps1 `
    -Source 'C:\ruta\a\OpenAI.Codex_26.820.9563.0_x64__...\' `
    -Destination "$env:LOCALAPPDATA\Programs\Codex Router Dev" `
    -StateRoot "$env:LOCALAPPDATA\Codex Router Dev" `
    -NoLaunch
```

El instalador rechaza programa o estado fuera de `LOCALAPPDATA` y cualquier
solapamiento entre fuente, destino y estado.

## Primer arranque y cuentas

1. Abra el router por su `ChatGPT.exe` independiente.
2. Confirme que la aplicación oficial puede permanecer abierta a la vez.
3. Abra el menú de perfil. La sesión actual de `%USERPROFILE%\.codex` aparece
   como cuenta primaria.
4. Seleccione **Add another subscription**.
5. Complete el inicio de sesión por código de dispositivo en el navegador.
6. Espere a que la segunda cuenta muestre identidad y límites.
7. Inicie un chat de prueba y compruebe que su resumen muestra la cuenta
   propietaria.
8. Haga un follow-up y confirme que conserva el mismo propietario.

No copie `auth.json` entre cuentas ni edite a mano el mapa de ownership.

### Conector de Chrome independiente (opt-in)

El portable actual deshabilita deliberadamente el alta y la baja del host
Native Messaging `com.openai.codexextension`. Ese nombre y su clave de registro
pertenecen a Codex oficial; reutilizarlos permitiría que el router sobrescribiera
el conector oficial al instalarse o lo eliminara al cerrarse/desinstalarse.

Las ubicaciones oficiales que deben conservar exactamente sus valores y hashes
son:

```text
HKCU\Software\Google\Chrome\NativeMessagingHosts\com.openai.codexextension
%LOCALAPPDATA%\OpenAI\extension\com.openai.codexextension.json
```

El upstream las administra con `reg add /f` y `reg delete /f`; ambas ramas
están deshabilitadas en el clon. El verificador de release debe comparar clave,
valores y manifest antes y después de instalar, iniciar, actualizar, hacer
rollback y desinstalar el router.

No importe manualmente el manifest oficial, no copie su extensión y no registre
ese nombre para el router. Codex oficial conserva su conector sin cambios.

El repositorio incluye ahora `chrome-extension/` y el host propio
`io.github.thedanixsx.codex_subscription_router`. Su instalación es separada y
opt-in porque exige el ID exacto asignado por Chrome Web Store. El instalador
lee `stateRoot` y el puerto dinámico de los manifests schema 2 de la instalación,
rechaza colisiones y registra solo HKCU. El desinstalador elimina únicamente un
valor y archivos que coincidan exactamente con su recibo de ownership.

```powershell
pwsh -NoProfile -File .\scripts\install_chrome_native_host.ps1 -ExtensionId <id-publicado>
pwsh -NoProfile -File .\scripts\uninstall_chrome_native_host.ps1
```

La fuente no equivale todavía a una promesa de paridad pública. La publicación,
firma y E2E de consumo en escritorio se controlan en
`docs/CHROME-CONNECTOR-RELEASE.md`.

## Verificación posterior

El instalador comprueba al menos:

- que existen `resources\app.asar`, `resources\codex.exe` y
  `resources\codex.real.exe`;
- que hay un launcher `ChatGPT.exe` o `Codex.exe`;
- que `codex-mux-build.json` es JSON válido y contiene versión de esquema,
  hash de ASAR de origen y hash del multiplexor;
- que `ChatGPT.exe --router-self-test`, ejecutado después de limpiar los
  overrides `CODEX_ROUTER_DATA_DIR`, `CODEX_MUX_HOME` y
  `CODEX_MUX_STATE_ROOT`, devuelve `root_source=sidecar` y el `state_root`
  absoluto exacto solicitado;
- que los hashes de `app.asar` y `codex.exe` de la fuente oficial no han
  cambiado durante la construcción.
- que la construcción no registra ni elimina el host Native Messaging oficial
  `com.openai.codexextension`, y que la clave y el manifest oficial conservan
  sus valores/hashes.

Para una verificación manual de procedencia:

```powershell
$routerRoot = Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'
Get-Content -Raw (Join-Path $routerRoot 'codex-mux-build.json') |
    ConvertFrom-Json |
    Format-List
Get-FileHash -Algorithm SHA256 (Join-Path $routerRoot 'resources\codex.exe')
Get-FileHash -Algorithm SHA256 (Join-Path $routerRoot 'resources\codex.real.exe')
```

La validación funcional completa se hace con el smoke test de Windows. Un
arranque correcto por sí solo no demuestra paridad de cuentas, failover,
plugins, resets o Computer Use.

## Actualizar o reconstruir

### Actualización del código del router

Con un checkout limpio:

```powershell
git fetch --all --prune
git status --short
git pull --ff-only
npm ci --ignore-scripts --no-audit --no-fund
```

Revise los cambios y ejecute los tests antes de reemplazar la instalación.
Después cierre **solo Codex Subscription Router**. El instalador detecta
procesos cuyo ejecutable pertenece al destino y se niega a terminarlos por su
cuenta.

```powershell
pwsh -NoProfile -File .\scripts\install_windows.ps1 -Force -NoLaunch
```

La aplicación oficial puede seguir abierta. `-Force` no borra la versión
anterior: la mueve a un backup y publica el nuevo árbol. El estado y las cuentas
permanecen fuera del destino.

### Actualización de la app oficial

Cuando Microsoft Store actualice Codex:

1. no reconstruya con `-AllowUntestedSource` por rutina;
2. registre versión y hashes del nuevo paquete;
3. compare ASAR, ejecutables y anclas con el baseline anterior;
4. actualice código y matriz de compatibilidad;
5. ejecute tests y `-DryRun`;
6. reconstruya con `-Force -NoLaunch`;
7. complete el smoke test antes de considerar compatible la versión.

El router no debe actualizarse copiando solo `app.asar`, `ChatGPT.exe` o
`codex.exe` desde la nueva instalación oficial. Todos los componentes deben
proceder de la misma versión.

## Backups y rollback

Antes de reemplazar un destino, el parcheador crea:

```text
%LOCALAPPDATA%\Programs\.codex-subscription-router-backups\
└── <timestamp>-<uuid>\
    └── Codex Subscription Router\
```

Si falla la publicación, el parcheador intenta restaurar automáticamente el
destino anterior. Si una verificación posterior a la publicación falla, el
instalador mueve el build fallido a
`<StateRoot>\failed-installations\<timestamp>` y restaura el backup. El acceso
directo anterior se respalda bajo
`<StateRoot>\shortcut-backups\<timestamp>`; el rollback solo lo elimina o
restaura tras comparar su `TargetPath`, para no tocar una personalización ajena.
Un fallo de la automatización COM del acceso directo se registra como warning y
no invalida una aplicación que ya superó la verificación. El manifest registra
atómicamente la ruta exacta del backup.

Para ver el plan y restaurar después el backup autenticado más reciente, cierre
solo el router y ejecute:

```powershell
pwsh -NoProfile -File .\scripts\rollback_windows.ps1 -WhatIf
pwsh -NoProfile -File .\scripts\rollback_windows.ps1
```

El comando valida raíz, manifest, hashes, árboles preservados y procesos por su
ruta ejecutable. El intercambio es transaccional: si falla la publicación,
restaura automáticamente la app que estaba activa. Estado, acceso directo y app
oficial no cambian.

La limpieza conserva la app activa y un backup por defecto:

```powershell
pwsh -NoProfile -File .\scripts\cleanup_windows.ps1 -WhatIf
pwsh -NoProfile -File .\scripts\cleanup_windows.ps1 -Confirm:$false
```

Los builds fallidos solo se incluyen al indicar `-IncludeFailedInstallations`.

## Desinstalación

El desinstalador preserva cuentas, perfil, logs y backups por defecto. También
desregistra únicamente integraciones de shell que continúen coincidiendo
exactamente con el esquema propiedad del router:

```powershell
pwsh -NoProfile -File .\scripts\uninstall_windows.ps1 -WhatIf
pwsh -NoProfile -File .\scripts\uninstall_windows.ps1
```

Para borrar además backups autenticados, añada `-RemoveBackups`. Para eliminar
de forma irreversible credenciales, cuentas, perfil y logs, añada
`-RemoveState`. Ambas decisiones aparecen juntas en la confirmación previa. La
app oficial y su paquete nunca son objetivos de estos comandos.

## Diagnóstico

Para un inventario de solo lectura de procesos por ruta, cuentas sin datos
personales, puerto aleatorio, versiones, espacio y logs redactados, ejecute:

```powershell
pwsh -NoProfile -File .\scripts\doctor_windows.ps1
```

El contrato completo y la medición de memoria/handles están en
[`WINDOWS-DIAGNOSTICS.md`](WINDOWS-DIAGNOSTICS.md). El doctor no inicia ni cierra
procesos y no limpia archivos.

### Falta Go

Si falta `-MuxPath` o `-LauncherPath`, el instalador exige Go 1.26 porque debe
compilar al menos uno de los dos ejecutables y `go.mod` usa esa versión. Instale
la toolchain, abra una terminal nueva y repita el dry run. No cambie la
directiva de `go.mod` para evitar el requisito.

### El destino ya existe

La ausencia de `-Force` es una protección. Verifique primero si el router está
abierto y, cuando quiera actualizarlo, use:

```powershell
pwsh -NoProfile -File .\scripts\install_windows.ps1 -Force -NoLaunch
```

### El router está ejecutándose

El instalador lista nombre y PID y se detiene; no mata procesos. Guarde el
trabajo, cierre únicamente la ventana independiente del router y repita. No es
necesario reiniciar la aplicación oficial ni la sesión que está coordinando la
compilación.

### Baseline no aprobado

Un hash distinto puede significar una actualización legítima, una instalación
mixta o un archivo alterado. Ejecute el inventario y compare la versión antes de
considerar `-AllowUntestedSource`. Aunque se use ese flag, cualquier ancla
ausente o duplicada sigue siendo un error fatal.

### Fallo durante npm o la compilación

En una instalación normal, el transcript se guarda bajo:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router Data\logs
```

Un dry run no debe crear `StateRoot`; su transcript se guarda en:

```text
%TEMP%\Codex Subscription Router\logs
```

El fallo previo a la publicación no toca el destino. Si ya había comenzado el
reemplazo, revise también la carpeta de backups y cualquier instalación fallida
preservada; no borre esos artefactos antes de resolver la causa.

### Arranca con la cuenta o el perfil equivocados

Compruebe que abrió el `ChatGPT.exe` del destino y no el de `WindowsApps`.
Revise `codex-mux-build.json` y las variables heredadas
`CODEX_ROUTER_DATA_DIR`, `CODEX_MUX_HOME`, `CODEX_MUX_STATE_ROOT`,
`CODEX_ELECTRON_USER_DATA_PATH` y `CODEX_HOME`. No modifique
`LOCALAPPDATA` globalmente para redirigir el router.

Para el diseño completo, aislamiento, fases y límites, consulte
[`WINDOWS-ARCHITECTURE.md`](WINDOWS-ARCHITECTURE.md).
