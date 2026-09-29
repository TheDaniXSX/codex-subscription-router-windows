# Entrega DEV de consumo y calibración

Fecha: 2026-09-29. Rama: `codex/usage-calibration`.
PR de desarrollo: <https://github.com/TheDaniXSX/codex-subscription-router-windows/pull/10>.

## Build instalada

La app se compiló desde `ad3a6fcb34ecb14da02732f6c28a3677b0c25e96`, con checkout
limpio. El ajuste posterior para rutas abreviadas de Windows afecta a las
validaciones del instalador y sus pruebas; no modifica el runtime instalado.

| Dato | Resultado |
| --- | --- |
| Paquete oficial utilizado | 26.924.2738.0 |
| ASAR / build oficial | 26.924.22138 / 11645 |
| CLI conservada | 0.158.0-alpha.2.1 |
| Canal | `development` |
| Ejecutable | `%LOCALAPPDATA%\CSR-Dev\app\ChatGPT.exe` |
| Estado | `%LOCALAPPDATA%\CSR-Dev\data` |
| Codex y SQLite home | `data\PrimaryHome` |
| Perfil Electron | `data\Profile` |
| Acceso directo | `Codex Subscription Router [DEV]` |
| Ventana observada | `ChatGPT [DEV]` |
| Control DEV / PROD | 60834 / 55876, ambos en `127.0.0.1` |
| Ledger al abrir | creado; 0 raíces, límite 5.000, unidad `Pro20` |
| Estimadores iniciales | corta y semanal, ambos `uncalibrated`, 0 muestras |

La instalación terminó con `-NoLaunch`. El verificador pasó **54 checks** y
el contrato del ASAR empaquetado confirmó el footer de uso. Después se abrió
DEV y su endpoint autenticado `/v1/usage/status` respondió sin error.
Los procesos y listeners de DEV y PROD coexistían. Los tokens son distintos.
Los siete hashes estáticos vigilados de producción permanecieron idénticos
tras instalar y abrir DEV; su proceso de control continuó siendo el mismo.

La nueva PrimaryHome no tenía `auth.json`. No se copiaron credenciales ni bases
de chats. Queda pendiente el inicio de sesión del usuario y el primer ensayo
con una cuenta real.

Hashes del paquete privado, para relacionar las pruebas con el artefacto local:

- ASAR parcheado: `ee0894041de00edfbe3e0473804281d153328770e12dd3169a71af30b44bbebe`.
- Mux: `ecd183642787699e5bd0ac8c313cb759e8fb23159492b35ffe14b45cded05311`.
- Launcher: `dcab2ff4fd8ecefdbeeba5d703bb7445443125ff3c50d6e56d04dbdc51c89224`.

## Pruebas ejecutadas

- `npm run check:windows`: Go tests/vet, **58 JS**, **140 Python Windows**, **9
  pruebas de publicación**, verificaciones de fuentes y conector.
- `go test -race` en usage/control/mux/spend; las últimas pruebas del mux para
  cuotas pendientes, reinicio y lecturas sin efectos sobre salud pasaron también.
- Smoke offline: los siete bloques pasaron, incluidos proxy transparente,
  integración de dos cuentas simuladas, contratos y ciclo de vida Windows.
- Bundles de la build exacta: 16 archivos JavaScript modificados parsean; prueba
  ejecutable del footer, identidades/memoización y selección autenticada.
- Checks de licencias transitivas, `go-licenses` y `govulncheck`; este último
  no encontró vulnerabilidades tras fijar `x/sys v0.44.0`.
- CI del código del runtime: pruebas portables en Linux/macOS/Windows y controles
  de calidad/seguridad aprobados. El job Windows detectó dos casos de rutas
  abreviadas del runner; su corrección queda cubierta por la revisión posterior.
  El estado autoritativo del SHA más reciente está en los checks del PR.

Los logs detallados permanecen localmente en `.artifacts/calibration-dev/` y el
transcript del instalador en `data\logs`. No se publican ejecutables, ASAR,
credenciales, datos de cuenta ni bases del ledger.

## Pendientes de aceptación

Las pruebas anteriores no acreditan todavía la correlación de agentes con una
cuenta real, aceptación visual/teclado/ventana estrecha ni precisión empírica del
calibrador. Seguir los casos de la [guía DEV](USAGE-CALIBRATION-DEV.md).
El ensayo de carga de 5.000 raíces × 20 operaciones, la UI global de
historial/exportación y los gráficos/alertas de deriva siguen pendientes.
La rama permanece en borrador y no se ha promovido a producción.
