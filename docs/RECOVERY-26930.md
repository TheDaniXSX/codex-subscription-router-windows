# Recuperación de c4eb2ea y adaptación a Codex 26.930

Fecha: 5 de octubre de 2026. Base única: `c4eb2eae4fc5c59987a901d373f7b58f5015c3a9`.

Se ha recuperado el checkout local antes de adaptar la compatibilidad. Los
commits posteriores y 39 archivos modificados/nuevos se conservaron en una
copia de recuperación externa, con bundle Git y hashes verificados; no se
incorporan a esta implementación. El GitHub remoto no se modifica por esta
operación local.

## Funcionalidades conservadas y excluidas

Se mantienen cuentas/login/Rename, modos Auto y suscripción estricta por nueva
inferencia, historial separado del gasto, plugins/perfiles/resets por cuenta,
atribución de la última petición aceptada, aislamiento Windows, launcher/icono,
control local privado y herramientas de construcción/verificación/rollback.
La especificación histórica está congelada en [docs/spec](spec/index.html).

No se incorporan costes API, calibración, tokens monetizados por turno, ledger
de uso, PROD/DEV, broker compartido, sincronización CAS ni protección de
borradores asociada a esa arquitectura. La palabra gasto sigue describiendo
la selección de suscripción propia del router, no una contabilidad nueva.

## Fuente exacta

| Componente | Identidad |
| --- | --- |
| Paquete oficial Windows x64 | `26.930.4958.0` |
| ASAR / build | `26.930.41038` / `13022` |
| CLI | `0.160.0` |
| ASAR SHA-256 | `644fec616f2fbd203266d806c2ed9a26869abb84e76fbd6f5a33469e8cfd1686` |
| Payload fuente | 3.596 archivos / 2.261.596.441 bytes |
| SHA-256 árbol fuente | `b816b22236e6881c82edadbccb44f7d464ea05fd92a7a4020e872921108b3320` |

El inventario pasó integridad y firmas. El paquete oficial es solo lectura.
No se omiten verificaciones por usar `--allow-untested-source`. Esta es la
última versión instalada observada; la comprobación informativa Store no pudo
confirmar otras versiones elegibles sin iniciar descarga.

## Comprobaciones de esta recuperación

El candidato se construyó con el núcleo de `c4eb2ea`, sin modificaciones en
la lógica Go de routing. La captura del CLI 0.160.0 contra un servidor local
ficticio confirmó que conserva los IDs de hilo/turno, el streaming y la
compactación con contexto completo que necesita esa lógica.

| Comprobación | Resultado |
| --- | --- |
| Núcleo Go y vet | PASS |
| Pruebas Windows Python | 140 de la suite completa y 4 del renderer nuevo PASS |
| Pruebas JS del router | 45 PASS |
| Pruebas de herramientas de release | 9 PASS, metadata histórica 0.2.0 |
| Visor de la especificación congelada | 6 PASS |
| Smoke Windows offline | 6 etapas PASS |
| JavaScript del ASAR real modificado | 11 bundles parsean |
| Menú empaquetado | Rename, login, Auto/manual, resets, perfiles, plugins y atribución PASS con fixtures |
| Verificador completo del candidato | 53 comprobaciones PASS |
| App-server real con homes vacíos | Passthrough y router PASS, sin autenticación ni inferencia real |
| Computer Use y Appshots | Contratos estáticos PASS, no aceptación interactiva |
| Icono embebido del launcher | PASS |
| Repositorio | Sin payload compilado ni credenciales versionados |

La actualización compatible de `brace-expansion` a 5.0.12 elimina los avisos
del audit npm sin cambiar `@electron/asar 4.3.0`: cero vulnerabilidades reportadas.

La adaptación del renderer conserva los nuevos elementos oficiales, las vistas
públicas/preview y los perfiles privados. Añade dependencias explícitas para que
los hooks del modal no aparezcan después de su primer render. El reset captura
la cuenta antes de esperar la respuesta; cambiar el selector no permite aplicar
el optimismo ni el callback de Primary a una redención secundaria. Las compras
desde una selección secundaria se bloquean porque el checkout nativo usa la
identidad de Primary. Son ajustes de compatibilidad y seguridad, no funciones
de analítica ni una nueva arquitectura.

La cualificación del payload y tests sintéticos no certifican uso de micrófono,
audio, Computer Use interactivo, consumo real de resets ni facturación. No se
ejecutan inferencias/login/redenciones reales durante la preparación. Una build
preparada tampoco significa que se haya reemplazado la aplicación instalada.

## Operación

El candidato comprobado está en `%USERPROFILE%\csr930\app`, con datos de prueba
independientes en `%USERPROFILE%\csr930-state`. No se creó acceso directo ni se
lanzó Electron. El manifiesto del candidato tiene SHA-256
`51887ba329778c19e1f79849e098e9722897dad087d7f111e7009296216d893c`.
Esto no sustituye la instalación habitual ni sus cuentas.

Construir con dependencias bloqueadas y PowerShell 7. Desde el host empaquetado
de Codex, Windows puede redirigir LocalAppData a una ruta de paquete más larga.
El preflight detuvo ese intento antes de copiar/publicar el payload. Para la
prueba se usó directamente el patcher con la ruta física corta, un puerto libre
reservado, ACLs privadas y el verificador completo, sin rebajar comprobaciones.
Para una instalación habitual, ejecutar el instalador desde un PowerShell
externo y elegir rutas que pasen el presupuesto de longitud real. Nunca matar
la app que aloja la sesión de actualización.

Una instalación anterior con schema 3 es un artefacto de la rama retirada: no
se convierte modificando el JSON a mano ni copiando binarios sueltos. Cuentas,
historiales y autenticación deben preservarse; cualquier sustitución real debe
tener un backup validado y su propia comprobación de lanzamiento.
