# Arquitectura del port para Windows

## Estado y alcance

Este documento describe el port nativo de **Codex Subscription Router** para
Windows. El proyecto reutiliza el multiplexor, el modelo de estado y las
inyecciones de interfaz del proyecto original, pero sustituye la instalación,
el lanzamiento, el aislamiento del perfil y el empaquetado específicos de
macOS.

El resultado es una copia local e independiente de la aplicación oficial. La
instalación oficial de Microsoft Store se usa exclusivamente como entrada de
compilación y permanece intacta. El port no distribuye binarios de OpenAI, no
adopta la identidad `OpenAI.Codex` y no escribe nunca en
`C:\Program Files\WindowsApps`.

> [!WARNING]
> Este es un proyecto no oficial y sensible a la versión de la aplicación de
> Codex. Una actualización oficial debe tratarse como una nueva entrada que
> requiere validación, reconstrucción y pruebas; no como una actualización
> binaria transparente del router.

## Invariantes de diseño

Estas condiciones no se relajan para hacer que una compilación desconocida
"funcione":

1. La carpeta del paquete oficial es de solo lectura.
2. La fuente y el destino deben ser rutas diferentes y el destino no puede
   estar dentro de `WindowsApps`.
3. El parcheador trabaja en *staging* y solo publica un árbol completo.
4. Una versión, hash o ancla no reconocidos detienen el proceso antes de
   instalar un resultado parcial.
5. `--allow-untested-source` omite únicamente la lista blanca de baseline; no
   omite la comprobación de anclas ni las verificaciones estructurales.
6. El Codex oficial se conserva como `codex.real.exe`; el multiplexor ocupa el
   nombre `codex.exe` esperado por el escritorio.
7. Las credenciales y el estado viven fuera de la carpeta de programa, de modo
   que una reconstrucción no los sobrescribe.
8. La actualización y el rollback se realizan por reemplazo de directorios,
   nunca modificando la copia que está ejecutándose.
9. El router no suplanta el publisher, la identidad de paquete, el protocolo ni
   las integraciones COM de OpenAI.

## Baseline validado

El baseline inicial del port es la instalación x64 observada el 27 de agosto de
2026:

| Campo | Valor |
| --- | --- |
| Nombre de paquete | `OpenAI.Codex` |
| Versión | `26.820.9563.0` |
| Arquitectura | `x64` |
| Familia | `OpenAI.Codex_2p2nqsd0c76g0` |
| Raíz típica | `C:\Program Files\WindowsApps\OpenAI.Codex_26.820.9563.0_x64__2p2nqsd0c76g0` |
| `app/resources/app.asar` SHA-256 | `e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a` |
| `app/resources/codex.exe` SHA-256 | `799ff77125c47b0736ceb36e9b33975bb93d4162bca663730f3a4c90faf2add9` |
| `app/ChatGPT.exe` SHA-256 | `4ec11307b67796338d666f40c431b2804e41669576d3bc350dece8703bf4a114` |
| Versión interna del paquete ASAR (`package.json`) | `26.820.71523` |
| `codexBuildNumber` interno | `7226` |
| Electron | `42.3.0` |

El hash del ASAR es parte de la barrera de compatibilidad. Los otros hashes
documentan la procedencia exacta y permiten diagnosticar instalaciones mixtas.
Además de los hashes, el parcheador exige las rutas esperadas, recuentos exactos
de anclas de JavaScript y la presencia de los ejecutables que va a sustituir.
Una coincidencia de texto y recuento no basta por sí sola: cada ancla se revisa
en su contexto semántico. En este baseline, por ejemplo, una función minificada
que coincidía con el parche macOS anterior pasó a ser un tokenizer, mientras
que el modal real de uso se movió a otros símbolos. Aplicar el reemplazo por
recuento habría parcheado código válido pero equivocado.

El ASAR no incluye source maps del renderer ni del proceso principal. Por ello
una entrada de compatibilidad se identifica por la tupla completa versión Appx,
versión interna de `package.json`, `codexBuildNumber`, arquitectura y hash del
ASAR, acompañada por anclas semánticas revisadas. No se permiten rangos o
comodines de versión.

La raíz del paquete oficial contiene `app\`; el parcheador normaliza ese
subdirectorio como **app-root**. En una instalación directa, app-root es el
propio `-Destination`, por lo que `ChatGPT.exe` y `resources\` quedan en la raíz
del destino. Las herramientas de empaquetado pueden envolver después ese
app-root en un package-tree con otro nivel `app\`.

## Componentes

### 1. Descubrimiento de la fuente oficial

El flujo normal consulta `Get-AppxPackage -Name OpenAI.Codex`, selecciona el
paquete instalado compatible y usa su `InstallLocation`. También se puede
proporcionar `--source` para pruebas reproducibles. Una fuente puede ser la raíz
del paquete Appx/MSIX o el layout normalizado que contiene `app/`.

La detección no toma posesión de la carpeta protegida, no cambia sus ACL y no
desregistra el paquete. Copiar desde `WindowsApps` es el único acceso necesario.

### 2. Parcheador de Windows

`scripts/patch_windows_app.py` orquesta una construcción transaccional:

1. Normaliza y valida la fuente.
2. Lee versión y hashes antes de crear el destino.
3. Verifica el baseline o exige `--allow-untested-source`.
4. Compila `codex-mux.exe`, salvo que se entregue un binario mediante `--mux`.
5. Crea un directorio temporal hermano del destino.
6. Copia allí el layout oficial completo.
7. Extrae `<app-root>/resources/app.asar` con la versión fijada de
   `@electron/asar`.
8. Comprueba cada ancla y aplica las inyecciones de perfil e interfaz.
9. Reempaqueta el ASAR, manteniendo fuera del archivo los módulos nativos que
   exige Electron.
10. Renombra `<app-root>/resources/codex.exe` a `codex.real.exe` e instala el
    multiplexor como `codex.exe`.
11. Compila el launcher independiente de escritorio, o acepta uno explícito
    mediante `--launcher`; renombra el original a `ChatGPT.real.exe` e instala
    el launcher como `ChatGPT.exe`.
12. Escribe el sidecar estricto del launcher con la raíz de estado absoluta.
13. Escribe `codex-mux-build.json` con la procedencia del artefacto.
14. Verifica el árbol en *staging*.
15. Publica el resultado por cambio de nombre. Si ya existía una instalación,
    primero la mueve a un backup recuperable.

`--dry-run` ejecuta el descubrimiento y las comprobaciones que no requieren
publicar un artefacto, sin modificar el destino ni el estado del usuario.

Además del ASAR, la copia contiene `resources\owl-app.ini` con
`UserDataDirectoryName=Codex`. El patcher sustituye esa línea exactamente una
vez por un nombre propio del router. Es una defensa adicional al perfil
inyectado mediante environment/switch y evita que el runtime Owl conserve el
nombre mutable oficial.

El bootstrap parcheado deshabilita el updater heredado, cambia el display name
y usa el AppUserModelID independiente
`com.openai.codex.subscription-router`. El patcher también verifica que el
registro de protocolo del ASAR sigue retornando inmediatamente en Windows y que
no aparece un registro autocontenido de `OpenProjectInCodex`. El manifest Appx
oficial no se copia, por lo que protocolo y Explorer continúan perteneciendo
exclusivamente a la app oficial.

Como capa unpackaged separada y opt-in, el gestor
`scripts/windows/Manage-ShellIntegration.ps1` puede registrar el esquema propio
`codex-router://` y dos verbos clásicos `OpenProjectInCodexRouter` bajo HKCU.
Estas claves apuntan al launcher independiente, tienen identidad y marcadores
de propiedad propios y se retiran únicamente mediante compare-and-delete. No
modifican la decisión anterior del ASAR ni reclaman `codex://`, CLSID o verbos
oficiales.

El baseline contiene además integración Chrome Native Messaging con el nombre
de host `com.openai.codexextension`, la clave
`HKCU\Software\Google\Chrome\NativeMessagingHosts\com.openai.codexextension`
y el manifest oficial
`%LOCALAPPDATA%\OpenAI\extension\com.openai.codexextension.json`. El upstream
ejecuta `reg add /f` y `reg delete /f`; si el clon heredara esas ramas podría
sobrescribir o eliminar el host oficial.

Para el perfil 26.820.9563.0, el patcher neutraliza de forma fail-safe las rutas
de manifest `RJ` y las operaciones de registro `sY`/`oY` en el ASAR copiado.
También desvía el JSON auxiliar `yJ` a la raíz del router, aunque ese archivo no
habilita por sí mismo la integración. No modifica la extensión, el manifest
nativo ni la clave oficial. Los hashes/valores pre y post de la clave y del
manifest son un gate P0 de build/release.

La integración heredada continúa siendo un gap deliberado. En paralelo existe
una implementación independiente y opt-in en `chrome-extension/` y
`cmd/chrome-native-host`: usa nombre, manifest, origen y registro HKCU propios;
valida otra vez el origen recibido por el host; y autentica el salto loopback
con el token que la extensión nunca recibe. El instalador toma `stateRoot` y
`controlPort` de los manifests schema 2 del router y el desinstalador usa
compare-and-delete. No se considera paridad pública hasta publicar el ID,
firmar el host y superar consumo de contexto e invariancia en una VM limpia.

### 3. Launcher de escritorio

La copia portable no debe compartir el perfil Chromium/Electron ni el ámbito
de instancia única de la aplicación oficial. El launcher de Windows conserva
el ejecutable oficial de escritorio como `<app-root>\ChatGPT.real.exe` y ocupa
`<app-root>\ChatGPT.exe`, que es el punto de entrada esperado por el layout.

La raíz persistente queda grabada durante el build en el sidecar
`<app-root>\resources\codex-router\launcher-config.json`. Es JSON UTF-8
estricto y versionado, no acepta campos desconocidos y contiene una ruta
absoluta. Ambos campos son obligatorios y una versión diferente de 1 falla
cerrado:

```json
{"schemaVersion":1,"stateRoot":"D:\\ruta\\absoluta"}
```

Al iniciarse:

- resuelve la raíz de datos con la precedencia `CODEX_ROUTER_DATA_DIR`,
  `CODEX_MUX_HOME`, `CODEX_MUX_STATE_ROOT`, sidecar y, por último,
  `%LOCALAPPDATA%\Programs\Codex Subscription Router Data`;
- crea la raíz y el subdirectorio `Profile`;
- antepone un `--user-data-dir` exclusivo a la línea de comandos;
- entrega al hijo las tres variables de raíz normalizadas,
  `CODEX_ROUTER_DATA_DIR`, `CODEX_MUX_HOME` y `CODEX_MUX_STATE_ROOT`, además de
  `CODEX_MUX_REAL_CODEX=<app-root>\resources\codex.real.exe` y
  `CODEX_ELECTRON_USER_DATA_PATH=<data-root>\Profile`;
- fija `CODEX_CLI_PATH=<app-root>\resources\codex.exe` para que el escritorio
  use el multiplexor; Appshots permanece desactivado por defecto con
  `CODEX_ROUTER_ENABLE_APPSHOTS=0` y solo el opt-in externo literal `1` habilita
  su puente experimental dentro de esta copia;
- conserva `CODEX_HOME`, para que la cuenta que ya utiliza el usuario siga
  siendo la cuenta primaria;
- reenvía los argumentos recibidos, espera al proceso real y propaga su código
  de salida.

El launcher resuelve `ChatGPT.real.exe`, `resources\codex.exe` y
`resources\codex.real.exe` como hermanos conocidos dentro del layout y exige
que sean archivos regulares diferentes. La variable de entorno y el switch de
perfil son compatibles con el bootstrap del baseline, que aplica
`app.setPath("userData", ...)` antes de adquirir el bloqueo de instancia única.
Por ello no se añade un mutex alternativo que pudiera perder activaciones o
URIs. El launcher preserva y vuelve a citar los argumentos con las reglas de
`CreateProcessW`, espera al proceso real y devuelve su mismo código de salida.
Rechaza un `--user-data-dir` entrante para que una activación externa no escape
del aislamiento. `--router-self-test` comprueba rutas y binarios hermanos sin
crear directorios ni iniciar el escritorio real.

### 4. Multiplexor `codex-mux.exe`

La aplicación de escritorio sigue abriendo una sola conexión JSON-RPC por
entrada/salida estándar y ejecutando `resources/codex.exe app-server`. El
binario sustituto distingue dos casos:

- para `app-server` interactivo inicia el multiplexor;
- para el resto de comandos actúa como *passthrough* hacia
  `codex.real.exe`, incluyendo entrada, salida, errores y código de salida.

En modo app-server abre el almacén persistente, inicia una instancia oficial
`codex.real.exe app-server` por cada cuenta habilitada y presenta al escritorio
una única sesión coherente.

```text
Codex Subscription Router (copia portable)
        |
        | JSON-RPC stdio: un app-server aparente
        v
<app-root>/resources/codex.exe  (codex-mux.exe)
        |
        +-- Primary ------> codex.real.exe app-server
        |                    CODEX_HOME = cuenta primaria
        |
        +-- Cuenta 2 -----> codex.real.exe app-server
        |                    CODEX_HOME = home aislado 2
        |
        +-- Cuenta N -----> codex.real.exe app-server
                             CODEX_HOME = home aislado N
        |
        +-- thread ID ----> propietario persistente
```

El ejecutable real se resuelve junto al wrapper como `codex.real.exe`. La
variable `CODEX_MUX_REAL_CODEX` existe para pruebas y diagnóstico.

### 5. Estado y aislamiento de cuentas

La cuenta primaria conserva el `CODEX_HOME` heredado o, si no existe,
`%USERPROFILE%\.codex`. Esto permite que la sesión seleccionada actualmente en
la aplicación oficial sea la primaria sin copiar ni exportar tokens.

La raíz del multiplexor se obtiene de `CODEX_MUX_HOME`. El launcher la fija en
la raíz de datos independiente; si se ejecuta `codex-mux.exe` sin launcher ni
variable, el fallback del binario es
`%USERPROFILE%\.codex-mux`.

Cada cuenta secundaria recibe su propio `CODEX_HOME` y `CODEX_SQLITE_HOME`:

```text
<mux-root>\
├── control-token
├── state.json
├── Profile\
│   └── ... perfil Chromium/Electron ...
└── accounts\
    └── <id-hex-16>\
        └── codex-home\
            ├── auth.json
            ├── config.toml
            └── ...
```

La configuración administrada se deriva de la cuenta primaria, excluyendo la
configuración del almacén de credenciales y la confianza de proyectos. Para
las cuentas aisladas, las credenciales de CLI y MCP se fuerzan a archivos en su
propio home.

El archivo `state.json` contiene metadatos de cuentas y el mapa persistente
`thread ID -> account ID`; no contiene el token OAuth de cada cuenta. El token
de control es aleatorio, tiene 32 bytes y se guarda como 64 caracteres
hexadecimales.

El instalador crea la raíz persistente antes del transcript y del token, le
aplica una DACL protegida sin herencia y deja `FullControl` únicamente al
usuario actual y a `SYSTEM`. La operación se verifica leyendo de nuevo la ACL;
un fallo impide continuar. Un mutex único por usuario serializa cualquier
instalación del router, aunque dos procesos hayan elegido destinos distintos.

Tras publicar, el installer limpia temporalmente los tres overrides de raíz y
ejecuta `ChatGPT.exe --router-self-test`. Solo acepta
`root_source=sidecar` y el `state_root` absoluto exacto. Así se demuestra que el
próximo arranque normal no depende del entorno que usó el proceso de build.

### 6. Enrutamiento de conversaciones

Las conversaciones existentes son *sticky*: cuando se conoce el ID del hilo,
su propietario se persiste y todos los turnos posteriores vuelven a esa cuenta.
Los hilos no migran para equilibrar carga ordinaria.

Para un hilo nuevo, solo son candidatos las cuentas habilitadas, conectadas con
ChatGPT y con capacidad semanal. La puntuación base es:

```text
porcentaje semanal restante / horas hasta el reset
```

Si no hay una fecha de reset válida se usa una ventana de siete días; la
ventana mínima es un minuto. Cada reset acumulado añade un 15 % a la puntuación,
con un máximo de tres créditos. Los metadatos de resets se consultan en
paralelo, se cachean cinco minutos y se consideran neutrales cuando no están
disponibles. Los desempates usan, en este orden, menor uso de ventana corta,
menor uso semanal, menor número de hilos y orden estable de creación.

Si el propietario está agotado antes de un turno, o el app-server devuelve un
error estructurado de límite, el router excluye esa cuenta, reanuda el historial
en otra con capacidad y actualiza el propietario. Si todas están agotadas,
devuelve un único error combinado con el siguiente reset conocido.

### 7. Interfaz inyectada y API de control

La extracción de `app.asar` permite reutilizar la interfaz oficial y añadir:

- gestión de cuentas desde el menú de perfil;
- cuota combinada y cuotas por cuenta;
- inicio de sesión mediante código de dispositivo y cierre de sesión;
- perfil combinado o individual;
- cuenta propietaria en el resumen de un hilo;
- selección de cuenta en Apps/MCP;
- consulta y consumo de resets para una cuenta concreta.

El renderer no accede directamente a credenciales. Habla con un servicio HTTP
local que el multiplexor enlaza únicamente a `127.0.0.1`. Cada instalación usa
un puerto CSPRNG en `49152..65535`, persistido de forma coherente en el sidecar
schema 2, el manifiesto y el ASAR local; no hay fallback de release a `48123`.
Las rutas privadas y el canal SSE requieren el token de control. CORS se
restringe al origen de la aplicación copiada. La API devuelve metadatos, estado
y resultados delegados, pero nunca los tokens OAuth.

Los marcadores privados usados para escoger una cuenta en operaciones de Apps
o MCP se eliminan antes de reenviar el JSON-RPC estricto al app-server real.
Las definiciones de plugins y la configuración MCP administrada se comparten;
las sesiones OAuth siguen estando aisladas por cuenta.

### 8. Aislamiento de caches y logs hardcodeados

El perfil Electron no controla todas las rutas que usa el baseline Windows. El
ASAR oficial contiene accesos directos a `%LOCALAPPDATA%\OpenAI\Codex` para
binarios/runtimes y a `%LOCALAPPDATA%\Codex\Logs` para logging. El launcher no
cambia `LOCALAPPDATA` globalmente, porque eso desviaría datos de Chromium y de
otros componentes de formas difíciles de auditar.

El parcheador sustituye de forma fail-safe el helper de cache `fF` en
`src-DJnwJvdz.js`, del que derivan tanto `bin` como `runtimes`, y las dos ramas
de logs repartidas entre `file-based-logger-j_XKovwV.js` y `worker.js`:

```text
%LOCALAPPDATA%\Programs\Codex Subscription Router Data\
├── runtime-cache\
│   ├── bin\
│   └── runtimes\
└── logs\
```

Las nuevas rutas se derivan de `CODEX_MUX_HOME`. Para el baseline se espera
exactamente un helper de cache y dos anclas de logs, una en cada bundle de
logging. Un recuento o contexto distinto aborta la construcción.

### 9. Empaquetado e identidad

El formato soportado inicialmente es **unpackaged/portable**. La instalación
directa coloca el app-root en
`%LOCALAPPDATA%\Programs\Codex Subscription Router`. Para preparar un árbol de
paquete, `packaging/windows/Build-Unpackaged.ps1` acepta tanto package-root como
app-root y normaliza la salida a `CodexSubscriptionRouter\app\...`;
`router-package.json` registra procedencia, hashes y launch target.

Existe una vía MSIX experimental mediante `Build-Msix.ps1`. Usa una identidad
propia (`CodexSubscriptionRouter.Local`), publisher no oficial y protocolo
`codex-router`. No reutiliza `OpenAI.Codex`, el publisher de OpenAI ni
`codex://`. La plantilla inicial tampoco declara el servidor COM, los menús de
contexto, asociaciones licenciadas u otras capacidades restringidas del
paquete oficial.

Por esas diferencias, el portable es la referencia funcional del port. El
MSIX solo se considera válido después de superar sus pruebas específicas de
firma, registro, lanzamiento, actualización y desinstalación.

## Directorios

| Elemento | Ruta por defecto | Propósito |
| --- | --- | --- |
| Fuente oficial | `InstallLocation` de `OpenAI.Codex` | Entrada de solo lectura |
| Repositorio | elegido por el desarrollador | Fuente, tests y herramientas |
| Staging | temporal, hermano del destino | Construcción antes de publicar |
| Programa portable | `%LOCALAPPDATA%\Programs\Codex Subscription Router` | Copia ejecutable independiente |
| Raíz de datos | `%LOCALAPPDATA%\Programs\Codex Subscription Router Data` | Datos que sobreviven a reconstrucciones y no quedan sujetos a la virtualización LocalAppData del MSIX oficial |
| Perfil desktop | `<data-root>\Profile` | Cookies, preferencias y estado Electron aislados |
| Estado mux | `<data-root>` | Cuentas, ownership y token de control |
| Cache de binarios/runtimes | `<data-root>\runtime-cache` | Evita escribir en caches oficiales hardcodeadas |
| Logs del escritorio | `<data-root>\logs` | Logging del router y transcript del instalador |
| Cuenta primaria | `%USERPROFILE%\.codex` o `CODEX_HOME` | Sesión primaria ya existente |
| Backups de programa | `<dest-parent>\.codex-subscription-router-backups\<timestamp>-<uuid>` | Rollback de una instalación anterior |

Ni el perfil ni el estado del multiplexor deben colocarse bajo el directorio de
programa: el reemplazo de una versión debe ser incapaz de borrarlos.

## Construcción, actualización y rollback

### Construcción inicial

La construcción se hace desde un clone/fork del proyecto, sobre una rama de
Windows. Las dependencias de build se fijan mediante `package-lock.json`; el
backend se compila desde `go.mod`. El artefacto publicado contiene binarios
copiados de la instalación oficial del mismo equipo y no se sube al repositorio
ni se redistribuye.

### Actualización

1. Se actualiza la aplicación oficial por Microsoft Store.
2. Se descubre y registra el nuevo número de versión y hashes.
3. Se revisan diferencias del ASAR y de los puntos de integración.
4. Se actualiza la lista de compatibilidad y, si procede, las anclas.
5. Se ejecutan tests unitarios, checks estáticos, build en *staging* y
   verificación del layout.
6. Se cierra únicamente el router; no es necesario cerrar esta sesión de Codex
   mientras se construye.
7. Se reconstruye con `--force`. La instalación anterior se mueve a backups y
   el nuevo árbol se publica.
8. Se hace el smoke test con una cuenta antes de añadir o habilitar más cuentas.
9. Solo después se elimina manualmente un backup antiguo.

El router portable no se actualiza copiando archivos sueltos desde una nueva
versión oficial. Mezclar `app.asar`, `codex.exe` y el runtime de versiones
distintas produce una instalación no soportada.

### Rollback automático

La publicación usa un directorio temporal hermano del destino para que los
cambios de nombre permanezcan en el mismo volumen. Si falla después de mover la
instalación existente, el parcheador aparta el resultado incompleto y restaura
el backup. El código no debe capturar ni ocultar un fallo de rollback.

### Rollback manual

Con el router cerrado:

1. identificar el backup cuyo `codex-mux-build.json` corresponde a la versión
   deseada;
2. mover la instalación actual a una carpeta de cuarentena, sin borrarla;
3. devolver el directorio de backup a la ruta de programa;
4. ejecutar el verificador y el smoke test;
5. conservar intactos `<data-root>` y `CODEX_HOME`.

Si el fallo está en los datos y no en el programa, el rollback binario no es
suficiente. Antes de tocar `state.json` o un home de cuenta se debe realizar una
copia y diagnosticar el archivo concreto. Nunca se restaura un `auth.json` sobre
otra cuenta.

## Fases de implementación y puertas de calidad

### Fase 0: inventario y baseline

- descubrir el paquete actual sin elevar privilegios;
- registrar versión, arquitectura, layout y hashes;
- comparar funcionalidades de macOS con sus equivalentes Windows;
- confirmar que ningún script escribe en `WindowsApps`.

**Puerta:** inventario reproducible y baseline aprobado.

### Fase 1: portabilidad del backend

- nombres de ejecutable por plataforma (`codex.real.exe`);
- propagación correcta de señales/cierre y códigos de salida;
- aislamiento de homes con rutas Windows;
- tests de estado, routing, failover y passthrough.

**Puerta:** `go test ./...`, `go vet ./...` y build x64 limpios.

### Fase 2: patcher transaccional

- descubrimiento Appx y override explícito;
- lista blanca de baseline y validación de anclas;
- ASAR reproducible y preservación de módulos nativos;
- staging, metadata, backups y rollback;
- `--dry-run` realmente no mutante.

**Puerta:** la fuente oficial mantiene sus hashes antes y después y un fallo
inyectado no deja un destino parcial.

### Fase 3: launcher y aislamiento del escritorio

- perfil de usuario independiente;
- raíz de estado independiente;
- quoting completo de Windows y propagación del exit code;
- coexistencia con la app oficial y bloqueo de dos instancias del router.

**Puerta:** oficial y router pueden abrirse sin compartir perfil ni estado.

### Fase 4: paridad de interfaz

- menú y login multicuenta;
- cuotas, resets y perfil combinado;
- ownership visible;
- selector de cuenta para Apps y MCP;
- validación de cada ancla contra el baseline Windows.

**Puerta:** tests de sintaxis/UI y smoke test con dos cuentas de prueba.

### Fase 5: integraciones Windows

- notificaciones, archivos adjuntos y Computer Use;
- conector de Chrome y Native Messaging con extensión/host propios; hasta
  entonces sus ramas de registro y desregistro permanecen deshabilitadas;
- protocolo independiente y accesos directos;
- evaluación separada de COM, menús del Explorador y asociaciones.

**Puerta:** cada integración se clasifica como soportada, degradada o pendiente;
no se declara paridad por el mero hecho de que el escritorio arranque.

### Fase 6: release y operación

- instalador idempotente;
- verificador y smoke test automatizado;
- CI sin artefactos propietarios;
- informe del baseline exacto;
- ensayo de actualización y rollback.

**Puerta:** instalación limpia, reconstrucción sobre una versión existente y
rollback comprobados en el mismo equipo.

## Límites y decisiones pendientes

- Las actualizaciones de Codex pueden romper anclas minificadas incluso cuando
  el aspecto de la interfaz no cambia.
- El empaquetado portable no hereda automáticamente protocolo, COM, menús del
  Explorador, licencias o asociaciones del paquete oficial.
- La firma Authenticode y el MSIX son capas de distribución, no sustitutos de
  las comprobaciones de procedencia y estructura.
- Computer Use debe validarse de extremo a extremo en Windows; no se presupone
  que la adaptación del renderer implique que el helper nativo funcione.
- El conector Chrome/native messaging no tiene paridad en el portable actual.
  No se debe registrar, copiar ni borrar `com.openai.codexextension`; ese host
  sigue siendo propiedad de la aplicación oficial.
- La configuración MCP compartida puede copiar secretos inline a homes
  secundarios; esos homes aíslan sesiones, pero no son fronteras absolutas de
  secretos.
- El historial combinado inicial está limitado por la paginación implementada
  en el multiplexor.

La matriz de paridad y el smoke test de Windows son las fuentes normativas para
distinguir lo implementado de lo que todavía requiere validación.
