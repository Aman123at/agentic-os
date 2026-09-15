// The Trash app (PLAN.md §4.3): the Dock's Trash opens the Finder's Trash view in
// a window of its own, where files are put back or the Trash is emptied.
import Finder from "../Finder";

export default function Trash() {
  return <Finder trashOnly />;
}
