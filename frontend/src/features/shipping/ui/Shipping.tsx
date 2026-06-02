import { useShipping } from '../model/useShipping'
import { BufferScanPhase } from './BufferScanPhase'
import { DonePhase } from './DonePhase'
import { DriverScanPhase } from './DriverScanPhase'
import { ShipPhase } from './ShipPhase'
import { ShippingErrorDialog } from './ShippingErrorDialog'

export function Shipping() {
  const shipping = useShipping()
  const { state } = shipping
  const isErrorOpen = state.errorMessage !== null

  // Persistent live region: announce the buffer count / ship summary to
  // screen-reader users.
  const announcement =
    state.phase === 'driver' && state.bufferBin
      ? `Буфер ${state.bufferBin.destination.name}: ${state.products.length} товаров готово к отгрузке`
      : state.phase === 'ship'
        ? `В буфере ${state.products.length} товаров, выбрано ${state.selectedProductIds.length}`
        : state.phase === 'done' && state.lastShipResult
          ? `Отгружено ${state.lastShipResult.productsShipped} товаров, машина отбыла`
          : ''

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>

      {state.phase === 'buffer' ? (
        <BufferScanPhase
          emptyBufferMessage={state.emptyBufferMessage}
          onScan={shipping.submitBufferScan}
          isScanning={shipping.isScanningBuffer}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'driver' && state.bufferBin ? (
        <DriverScanPhase
          bufferBin={state.bufferBin}
          productCount={state.products.length}
          onScan={shipping.submitDriverScan}
          onBack={shipping.backToBuffer}
          isScanning={shipping.isScanningDriver}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'ship' && state.bufferBin && state.dispatch ? (
        <ShipPhase
          bufferBin={state.bufferBin}
          products={state.products}
          dispatch={state.dispatch}
          selectedProductIds={state.selectedProductIds}
          destinationMismatch={state.destinationMismatch}
          partialMessage={state.partialMessage}
          onToggleProduct={shipping.toggleProduct}
          onScanProduct={shipping.scanProduct}
          onClearSelection={shipping.clearSelection}
          onShipBulk={shipping.shipBulk}
          onShipSpot={shipping.shipSpot}
          onBack={shipping.backToDriver}
          isShipping={shipping.isShipping}
          isBlocked={isErrorOpen}
        />
      ) : state.phase === 'done' && state.dispatch && state.lastShipResult ? (
        <DonePhase
          dispatch={state.dispatch}
          result={state.lastShipResult}
          onReset={shipping.reset}
        />
      ) : null}

      <ShippingErrorDialog
        message={state.errorMessage}
        onDismiss={shipping.dismissError}
      />
    </div>
  )
}
