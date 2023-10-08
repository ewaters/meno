* term.go NewMeno
  * Meno.resized()
    * driver.ResizeWindow()
      * driver.go lineWrapCall.run()
        * line_wrapper.go lineWrapper.Run
          * Reads blocks from blockC
    * driver.WatchLines


## LineWrapper

line_wrapper.go lineWrapper

### Run:

Args:

* `blockC chan blocks.Block`
* `wrapEventC chan wrapEvent`

Goals:

* Read from `blockC` and emit a series of `visibleLine` objects.
* Optionally emits the total number of visible lines to `wrapEventC`

Blocked by:

* `wrapEventC`: every new line will trigger a `wrapEvent` to `wrapEventC`
* `sub.respC`: If a line is wanted by an active subscription, it will block on
  sending.
* `req.respC`: any `chanRequest` must be able to receive the response
  * This will always be true as requests are only through `sendRequest`

## LineWrapCall

driver.go lineWrapCall

### Run

Args:

* backfill to ID

Goals:

* Reads from the driver `blockEventC` and passes it to a `lineWrapper`
* When the width changes, the caller closes the `lineWrapCall` which returns a
  block ID.
* New `lineWrapCall` will read from the last block ID (if present) and send to
  the `lineWrapper.blockC`.

Blocked by:

* `blockC`: this must always be drained otherwise new blocks will not drain from
  the `driver.blockEventC`

## WatchLines

driver.go

Goals:

* To manage an `eventFilter` which subscribes to `visibleLines` from the current
  `lineWrapper` and delivers them to `eventC`.

Actions:

* `wrapCall.wraper.SubscribeLines` with `lineC`

Blocked by:

* `driver.eventC`: must be able to write to `eventC` to drain `lineC`

## Issue

A window resize while still reading the data freezes.

* block.go Reader reads data and send is to reqC
* bytesRead and readDone both call newBlock which blocks on eventC
* Driver.ResizeWindow creates a new lineWrapCall and runs it
* lineWrapCall.run will backfill via block.Reader.GetBlockRange

What reads the block.Reader.eventC?

* block.Reader eventC is the Driver.blockEventC
* This is only read in lineWrapCall

So, the backfill can't fetch the data because the Reader.Run reqC loop is
blocked on sending an event to eventC, which isn't being watched yet since we're
intentionally backfilling before processing new blocks.

