# Codex 26.924: implementación y cualificación

Fecha: 2026-09-28. Distribución: **solo código fuente, preview**; no instalador,
ASAR, ejecutables oficiales, cuentas ni tokens en GitHub.

## Identidad del input

| Componente | Valor comprobado |
| --- | --- |
| Paquete Windows x64 | `OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0` |
| ASAR / build | `26.924.22138` / `11645` |
| CLI real | `0.158.0-alpha.2.1` |
| SHA-256 ASAR original | `89fba67324ffb8dd54ccf13b6f097172e697549eeb1f26396f86f972c10c5b0c` |
| CUA | 2.366 archivos, 251.062.420 bytes |
| SHA-256 árbol CUA | `355f5b4661571019ff76e671289320694585e6e3c5fabfddd27f77f6a48f8cb9` |
| Node manifest / binario | `24.21.0-cua.1` / `24.21.0` |
| Runtime CUA / paquete | `0.0.24/20260924074400-f52ea85e2a98` / `@oai/cua 0.2.5` |

El inventario completo del paquete fuente pasó firmas y hashes: 3.583 archivos,
2.169.517.103 bytes. La instalación oficial solo se lee.

## Cambios implementados

- Perfil exacto con rechazo de versión/hash/anchors desconocidos o duplicados.
- Bootstrap/updater: conserva `initialize()` para resolver la política, con
  updater desactivado; elimina la recuperación que podía iniciarlo.
- Nuevas rutas de caché/logs, AppUserModelID y aislamiento de native messaging.
  Se mantiene `CODEX_CLI_PATH` para seleccionar el mux, no el core registrado.
- Adaptación al nuevo chunk lazy del menú: Rename, Auto/suscripción, Usage/reset,
  perfil, plugins y atribución de la última inferencia. Perfil/plugins declaran
  su dependencia de los helpers y el hilo espera antes de suscribirse a eventos.
- Corrección del refresco del perfil y conservación de los wrappers React.
- CUA intacto; manifest con sufijo `-cua.1` validado contra el binario exacto.
  Appshots mantiene elegibilidad upstream y exige opt-in literal `1` más bridge.
- PowerShell 7 obligatorio para ciclo de vida y verificación completa. Preflight
  de rutas del payload proyectadas en destino/staging antes de copiar/compilar.
- Parseo obligatorio de **todos** los bundles JS modificados antes de publicar.

No cambia el backend de gasto: Auto decide en cada inferencia; la suscripción
explícita es estricta incluso en tareas existentes y subagentes. Historial y
plugins mantienen su propietario. Se conserva el límite de petición de 128 MiB.

## Evidencia automatizada local

| Prueba | Resultado |
| --- | --- |
| Python Windows | 129/129 PASS |
| JavaScript UI | 45/45 PASS |
| Release fuente | 9/9 PASS |
| Go tests / vet | PASS |
| Smoke offline Windows | 6 etapas PASS |
| Sintaxis de bundles reales modificados | 13/13 PASS |
| Contrato del menú empaquetado | PASS: Rename, routing, Usage, resets scoped, perfil/plugins e inicialización lazy |
| App-server real, passthrough y router | PASS: initialize, initialized, account/read sin cuenta, thread/list vacío, cierre por EOF |
| Repositorio sin payloads/secretos y metadata release | PASS |
| Icono del launcher / npm audit | PASS / 0 vulnerabilidades |

Los tests de reset usan créditos sintéticos; no se gastan créditos reales.
El smoke real usa homes y entorno nuevos, sin iniciar la UI ni inferencia.
Los tests detectaron un error de sintaxis del bootstrap, ya corregido; también
se corrigieron dos supuestos del harness (re-render y token fixture) y su
selección de puerto para equipos con rango efímero personalizado.

## Integridad y activación

26.924 vuelve a incluir `INTEGRITY/ELECTRONASAR`. El original firmado se conserva
en `ChatGPT.original.exe`; el runtime `ChatGPT.real.exe` solo cambia el digest
de 64 caracteres previsto para el ASAR parcheado. El derivado **no conserva la
firma Authenticode de OpenAI**. No se desactivan los fuses de integridad.

La build se prepara en un destino corto separado mientras sigue abierta la
anterior. La activación local debe validar manifest/hashes, estado y puerto,
esperar al cierre voluntario y hacer un intercambio de directorios en el mismo
volumen, con backup y reversión si falla antes del lanzamiento. Nunca terminar
procesos para sustituir la app desde la propia sesión activa. La activación
solo está confirmada cuando la versión instalada y su health se verifican.

## Publicación y límites

Publicar únicamente los scripts, tests y documentación revisados en el GitHub
personal `TheDaniXSX/codex-subscription-router-windows`; comprobar el SHA remoto
de `main` y consultar CI del commit. Los resultados CI no se infieren de los
tests locales. No es necesario crear una release, tag ni instalador.

La prueba live del escritorio tras el cambio, voz con audio y Computer Use con
captura/interacción quedan **separadas** del build y del protocolo probado.
No se ejecutaron ni se presentan como aprobadas. La publicación de esta preview
fuente no declara aceptación E2E ni reemplaza el informe histórico 0.2.0.
